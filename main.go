package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	Mode   string `json:"mode"` // "fast" or "quality"
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
	Mode       string      `json:"mode"`
}

type errResp struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// -------------------- Cache (in-memory TTL) --------------------

type cacheItem struct {
	val AnswerResponse
	exp time.Time
}

var (
	cacheMu  sync.RWMutex
	cacheMap = map[string]cacheItem{}
)

func cacheKey(prompt, mode string) string {
	sum := sha256.Sum256([]byte(mode + "::" + prompt))
	return fmt.Sprintf("%x", sum[:])
}

func cacheGet(key string) (AnswerResponse, bool) {
	cacheMu.RLock()
	it, ok := cacheMap[key]
	cacheMu.RUnlock()
	if !ok || time.Now().After(it.exp) {
		return AnswerResponse{}, false
	}
	return it.val, true
}

func cacheSet(key string, val AnswerResponse, ttl time.Duration) {
	cacheMu.Lock()
	cacheMap[key] = cacheItem{val: val, exp: time.Now().Add(ttl)}
	cacheMu.Unlock()
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
			prompt := "Answer the user clearly and directly.\n" +
				"Prefer correct, concise explanations and practical examples when helpful.\n\n" +
				"User:\n" + userPrompt

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

	// Stable order: fastest first (nice for UI, also helps fast mode pick quickly if needed)
	sort.Slice(cands, func(i, j int) bool { return cands[i].LatencyMs < cands[j].LatencyMs })
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
	b.WriteString("Rules: be correct, remove contradictions, be concise, no fluff.\n")
	b.WriteString("If a step-by-step explanation is helpful, include it.\n\n")
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

// Fast heuristic: skip judge+synth if answers are “close enough”
func fastPick(cands []Candidate) Candidate {
	// Prefer the one with more structure (newlines/bullets), then shorter latency
	best := cands[0]
	bestNL := strings.Count(best.Text, "\n")
	for _, c := range cands[1:] {
		nl := strings.Count(c.Text, "\n")
		if nl > bestNL+1 {
			best = c
			bestNL = nl
			continue
		}
	}
	return best
}

func shouldSkipJudgeInFastMode(cands []Candidate) bool {
	if len(cands) < 2 {
		return true
	}
	// If lengths are similar, they likely agree; skip judge/synth for speed.
	a, b := len(cands[0].Text), len(cands[1].Text)
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 350
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

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode != "quality" {
		mode = "fast"
	}

	// Cache
	key := cacheKey(req.Prompt, mode)
	if v, ok := cacheGet(key); ok {
		v.Cached = true
		writeJSON(w, http.StatusOK, v)
		return
	}

	// Time budgets
	var (
		providers []provider
		timeout   time.Duration
		cacheTTL  time.Duration
	)
	if mode == "quality" {
		// Quality: 3 providers + judge + synth
		providers = []provider{
			{name: "llama3.2", model: "llama3.2"},
			{name: "qwen2.5", model: "qwen2.5"},
			{name: "mistral", model: "mistral"},
		}
		timeout = 120 * time.Second
		cacheTTL = 30 * time.Minute
	} else {
		// Fast: 2 providers; judge/synth only when needed
		providers = []provider{
			{name: "llama3.2", model: "llama3.2"},
			{name: "qwen2.5", model: "qwen2.5"},
		}
		timeout = 45 * time.Second
		cacheTTL = 10 * time.Minute
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	cands := fanOut(ctx, providers, req.Prompt)
	if len(cands) == 0 {
		writeJSON(w, http.StatusBadGateway, errResp{Error: "no model responses (is Ollama running on localhost:11434?)"})
		return
	}

	// If only 1 came back, just return it.
	if len(cands) == 1 {
		resp := AnswerResponse{Final: cands[0].Text, Candidates: cands, Cached: false, Mode: mode}
		cacheSet(key, resp, cacheTTL)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// FAST MODE: often skip judge+synth
	if mode == "fast" && shouldSkipJudgeInFastMode(cands) {
		best := fastPick(cands)
		resp := AnswerResponse{Final: best.Text, Candidates: cands, Cached: false, Mode: mode}
		cacheSet(key, resp, cacheTTL)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Judge + (optional) synth
	judgeModel := "llama3.2"
	scores, err := judgeCandidates(ctx, judgeModel, req.Prompt, cands)
	if err != nil {
		// Fallback: pick best heuristic
		best := fastPick(cands)
		resp := AnswerResponse{Final: best.Text, Candidates: cands, Cached: false, Mode: mode}
		cacheSet(key, resp, cacheTTL)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Take top 2 for synthesis
	top := []Candidate{cands[scores[0].Idx]}
	if len(scores) > 1 {
		top = append(top, cands[scores[1].Idx])
	}

	final := cands[scores[0].Idx].Text
	// Only synthesize in quality mode OR when judge thinks top answers differ meaningfully
	if mode == "quality" {
		if merged, err := synthesize(ctx, judgeModel, req.Prompt, top); err == nil && strings.TrimSpace(merged) != "" {
			final = merged
		}
	} else {
		// Fast mode: synthesize only if top answer is short or unstructured
		if len(final) < 500 {
			if merged, err := synthesize(ctx, judgeModel, req.Prompt, top); err == nil && strings.TrimSpace(merged) != "" {
				final = merged
			}
		}
	}

	resp := AnswerResponse{Final: final, Candidates: cands, Cached: false, Mode: mode}
	cacheSet(key, resp, cacheTTL)
	writeJSON(w, http.StatusOK, resp)
}

func main() {
	http.HandleFunc("/answer", handleAnswer)
	log.Println("Go backend listening on :8080 (expects Ollama on :11434)")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
