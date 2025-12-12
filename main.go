package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type AnswerRequest struct {
	Prompt string `json:"prompt"`
}

type Candidate struct {
	Provider  string `json:"provider"`
	Text      string `json:"text"`
	LatencyMs int64  `json:"latency_ms"`
}

type AnswerResponse struct {
	Final      string      `json:"final"`
	Candidates []Candidate `json:"candidates"`
	Cached     bool        `json:"cached"`
}

type errResp struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// -------------------- Ollama client --------------------

type ollamaGenerateReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type ollamaGenerateResp struct {
	Response string `json:"response"`
}

func ollamaGenerate(ctx context.Context, model, prompt string) (string, error) {
	body, _ := json.Marshal(ollamaGenerateReq{
		Model:  model,
		Prompt: prompt,
		Stream: false,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", "http://localhost:11434/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	cli := &http.Client{Timeout: 120 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ollama non-2xx: %s", resp.Status)
	}

	var out ollamaGenerateResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}

	return strings.TrimSpace(out.Response), nil
}

// -------------------- Ensemble logic --------------------

type provider struct {
	name  string
	model string
}

func fanOut(ctx context.Context, providers []provider, userPrompt string) []Candidate {
	type result struct {
		c   Candidate
		err error
	}
	ch := make(chan result, len(providers))

	var wg sync.WaitGroup
	wg.Add(len(providers))

	for _, p := range providers {
		p := p
		go func() {
			defer wg.Done()
			start := time.Now()

			prompt := "Answer the user clearly and directly.\n\nUser:\n" + userPrompt
			text, err := ollamaGenerate(ctx, p.model, prompt)

			lat := time.Since(start).Milliseconds()
			if err != nil || strings.TrimSpace(text) == "" {
				ch <- result{err: err}
				return
			}
			ch <- result{c: Candidate{Provider: p.name, Text: text, LatencyMs: lat}}
		}()
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	cands := make([]Candidate, 0, len(providers))
	for r := range ch {
		if r.err == nil && strings.TrimSpace(r.c.Text) != "" {
			cands = append(cands, r.c)
		}
	}
	return cands
}

type scored struct {
	Idx   int
	Score int
	Notes string
}

func judgeCandidates(ctx context.Context, judgeModel string, userPrompt string, cands []Candidate) ([]scored, error) {
	if len(cands) == 0 {
		return nil, errors.New("no candidates")
	}

	var b strings.Builder
	b.WriteString("You are a strict evaluator.\n")
	b.WriteString("Score each answer 0-10 for correctness + usefulness. Penalize hallucinations.\n")
	b.WriteString("Return ONLY valid JSON array like: [{\"idx\":0,\"score\":7,\"notes\":\"...\"}, ...]\n\n")
	b.WriteString("User prompt:\n")
	b.WriteString(userPrompt)
	b.WriteString("\n\nAnswers:\n")
	for i, c := range cands {
		b.WriteString(fmt.Sprintf("\n[%d] (%s)\n%s\n", i, c.Provider, c.Text))
	}

	raw, err := ollamaGenerate(ctx, judgeModel, b.String())
	if err != nil {
		return nil, err
	}

	var arr []struct {
		Idx   int    `json:"idx"`
		Score int    `json:"score"`
		Notes string `json:"notes"`
	}
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil, fmt.Errorf("judge returned non-JSON: %s", raw)
	}

	out := make([]scored, 0, len(arr))
	for _, x := range arr {
		if x.Idx >= 0 && x.Idx < len(cands) {
			out = append(out, scored{Idx: x.Idx, Score: x.Score, Notes: x.Notes})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("judge produced no usable scores")
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

func synthesize(ctx context.Context, synthModel string, userPrompt string, top []Candidate) (string, error) {
	var b strings.Builder
	b.WriteString("Combine the best parts of the answers below into ONE final answer.\n")
	b.WriteString("Rules: be correct, remove contradictions, be concise, no fluff.\n\n")
	b.WriteString("User prompt:\n")
	b.WriteString(userPrompt)
	b.WriteString("\n\nAnswers:\n")
	for _, c := range top {
		b.WriteString("\n---\n")
		b.WriteString(c.Provider + ":\n")
		b.WriteString(c.Text + "\n")
	}
	return ollamaGenerate(ctx, synthModel, b.String())
}

// -------------------- HTTP handler --------------------

func handleAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errResp{Error: "POST only"})
		return
	}

	var req AnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp{Error: "bad json"})
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, errResp{Error: "prompt required"})
		return
	}

	// Whole pipeline timeout (tune later)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	providers := []provider{
		{name: "llama3.2", model: "llama3.2"},
		{name: "qwen2.5", model: "qwen2.5"},
		{name: "mistral", model: "mistral"},
	}

	cands := fanOut(ctx, providers, req.Prompt)
	if len(cands) == 0 {
		writeJSON(w, http.StatusBadGateway, errResp{Error: "no model responses (is Ollama running on localhost:11434?)"})
		return
	}

	// Judge using one model
	scores, err := judgeCandidates(ctx, "llama3.2", req.Prompt, cands)
	if err != nil {
		// Fallback: fastest candidate
		sort.Slice(cands, func(i, j int) bool { return cands[i].LatencyMs < cands[j].LatencyMs })
		writeJSON(w, http.StatusOK, AnswerResponse{Final: cands[0].Text, Candidates: cands, Cached: false})
		return
	}

	// Top-2 synthesis
	top := []Candidate{cands[scores[0].Idx]}
	if len(scores) > 1 {
		top = append(top, cands[scores[1].Idx])
	}

	final, err := synthesize(ctx, "llama3.2", req.Prompt, top)
	if err != nil || strings.TrimSpace(final) == "" {
		final = cands[scores[0].Idx].Text
	}

	writeJSON(w, http.StatusOK, AnswerResponse{Final: final, Candidates: cands, Cached: false})
}

func main() {
	http.HandleFunc("/answer", handleAnswer)
	log.Println("Go backend listening on :8080 (expects Ollama on :11434)")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
