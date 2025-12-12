package main

import (
	"bufio"
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

type ollamaStreamResp struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
	// there are other fields, we ignore them
}

func ollamaGenerate(ctx context.Context, model, prompt string) (string, error) {
	body, _ := json.Marshal(ollamaGenerateReq{Model: model, Prompt: prompt, Stream: false})

	req, err := http.NewRequestWithContext(ctx, "POST", "http://localhost:11434/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	cli := &http.Client{Timeout: 180 * time.Second}
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

// Stream: calls Ollama with stream:true, invokes onDelta for each chunk.
// Returns the full concatenated text too.
func ollamaGenerateStream(ctx context.Context, model, prompt string, onDelta func(string) error) (string, error) {
	body, _ := json.Marshal(ollamaGenerateReq{Model: model, Prompt: prompt, Stream: true})

	req, err := http.NewRequestWithContext(ctx, "POST", "http://localhost:11434/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	cli := &http.Client{Timeout: 0} // rely on ctx
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ollama non-2xx: %s", resp.Status)
	}

	sc := bufio.NewScanner(resp.Body)
	// Increase scanner buffer for safety
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 4*1024*1024)

	var full strings.Builder
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var chunk ollamaStreamResp
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			return "", fmt.Errorf("ollama stream decode error: %v", err)
		}
		if chunk.Response != "" {
			full.WriteString(chunk.Response)
			if onDelta != nil {
				if err := onDelta(chunk.Response); err != nil {
					return full.String(), err
				}
			}
		}
		if chunk.Done {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return full.String(), err
	}

	return strings.TrimSpace(full.String()), nil
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

	// fastest first (nice for UI)
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

func synthPrompt(userPrompt string, top []Candidate) string {
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
	return b.String()
}

// Fast heuristic: pick the one with more structure (newlines), else fastest
func fastPick(cands []Candidate) Candidate {
	best := cands[0]
	bestNL := strings.Count(best.Text, "\n")
	for _, c := range cands[1:] {
		nl := strings.Count(c.Text, "\n")
		if nl > bestNL+1 {
			best = c
			bestNL = nl
		}
	}
	return best
}

func shouldSkipJudgeInFastMode(cands []Candidate) bool {
	if len(cands) < 2 {
		return true
	}
	a, b := len(cands[0].Text), len(cands[1].Text)
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 350
}

// -------------------- NDJSON streaming helpers --------------------

type streamMsg struct {
	Type string `json:"type"`           // "status" | "delta" | "meta" | "error"
	Text string `json:"text,omitempty"` // for status/delta/error
	Meta any    `json:"meta,omitempty"` // for meta
}

func writeNDJSON(w http.ResponseWriter, v streamMsg) error {
	b, _ := json.Marshal(v)
	_, err := w.Write(append(b, '\n'))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return err
}

// -------------------- Handlers --------------------

// Non-stream JSON endpoint (kept for compatibility)
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

	key := cacheKey(req.Prompt, mode)
	if v, ok := cacheGet(key); ok {
		v.Cached = true
		writeJSON(w, http.StatusOK, v)
		return
	}

	var (
		providers []provider
		timeout   time.Duration
		cacheTTL  time.Duration
	)
	if mode == "quality" {
		providers = []provider{
			{name: "llama3.2", model: "llama3.2"},
			{name: "qwen2.5", model: "qwen2.5"},
			{name: "mistral", model: "mistral"},
		}
		timeout = 120 * time.Second
		cacheTTL = 30 * time.Minute
	} else {
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

	if len(cands) == 1 {
		resp := AnswerResponse{Final: cands[0].Text, Candidates: cands, Cached: false, Mode: mode}
		cacheSet(key, resp, cacheTTL)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	if mode == "fast" && shouldSkipJudgeInFastMode(cands) {
		best := fastPick(cands)
		resp := AnswerResponse{Final: best.Text, Candidates: cands, Cached: false, Mode: mode}
		cacheSet(key, resp, cacheTTL)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	judgeModel := "llama3.2"
	scores, err := judgeCandidates(ctx, judgeModel, req.Prompt, cands)
	if err != nil {
		best := fastPick(cands)
		resp := AnswerResponse{Final: best.Text, Candidates: cands, Cached: false, Mode: mode}
		cacheSet(key, resp, cacheTTL)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	top := []Candidate{cands[scores[0].Idx]}
	if len(scores) > 1 {
		top = append(top, cands[scores[1].Idx])
	}

	final := cands[scores[0].Idx].Text
	if mode == "quality" {
		merged, err := ollamaGenerate(ctx, judgeModel, synthPrompt(req.Prompt, top))
		if err == nil && strings.TrimSpace(merged) != "" {
			final = merged
		}
	} else {
		if len(final) < 500 {
			merged, err := ollamaGenerate(ctx, judgeModel, synthPrompt(req.Prompt, top))
			if err == nil && strings.TrimSpace(merged) != "" {
				final = merged
			}
		}
	}

	resp := AnswerResponse{Final: final, Candidates: cands, Cached: false, Mode: mode}
	cacheSet(key, resp, cacheTTL)
	writeJSON(w, http.StatusOK, resp)
}

// Streaming NDJSON endpoint
func handleAnswerStream(w http.ResponseWriter, r *http.Request) {
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

	// NDJSON streaming headers
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	key := cacheKey(req.Prompt, mode)
	if v, ok := cacheGet(key); ok {
		_ = writeNDJSON(w, streamMsg{Type: "status", Text: "cache hit"})
		_ = writeNDJSON(w, streamMsg{Type: "delta", Text: v.Final})
		v.Cached = true
		_ = writeNDJSON(w, streamMsg{Type: "meta", Meta: v})
		return
	}

	var (
		providers []provider
		timeout   time.Duration
		cacheTTL  time.Duration
	)
	if mode == "quality" {
		providers = []provider{
			{name: "llama3.2", model: "llama3.2"},
			{name: "qwen2.5", model: "qwen2.5"},
			{name: "mistral", model: "mistral"},
		}
		timeout = 120 * time.Second
		cacheTTL = 30 * time.Minute
	} else {
		providers = []provider{
			{name: "llama3.2", model: "llama3.2"},
			{name: "qwen2.5", model: "qwen2.5"},
		}
		timeout = 45 * time.Second
		cacheTTL = 10 * time.Minute
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	_ = writeNDJSON(w, streamMsg{Type: "status", Text: "running models..."})
	cands := fanOut(ctx, providers, req.Prompt)
	if len(cands) == 0 {
		_ = writeNDJSON(w, streamMsg{Type: "error", Text: "no model responses (is Ollama running on localhost:11434?)"})
		return
	}

	// FAST shortcut
	if mode == "fast" && len(cands) >= 2 && shouldSkipJudgeInFastMode(cands) {
		best := fastPick(cands)
		_ = writeNDJSON(w, streamMsg{Type: "status", Text: "fast path (no judge)"})
		_ = writeNDJSON(w, streamMsg{Type: "delta", Text: best.Text})

		resp := AnswerResponse{Final: best.Text, Candidates: cands, Cached: false, Mode: mode}
		cacheSet(key, resp, cacheTTL)
		_ = writeNDJSON(w, streamMsg{Type: "meta", Meta: resp})
		return
	}

	judgeModel := "llama3.2"
	_ = writeNDJSON(w, streamMsg{Type: "status", Text: "judging candidates..."})

	scores, err := judgeCandidates(ctx, judgeModel, req.Prompt, cands)
	if err != nil {
		best := fastPick(cands)
		_ = writeNDJSON(w, streamMsg{Type: "status", Text: "judge failed; using best guess"})
		_ = writeNDJSON(w, streamMsg{Type: "delta", Text: best.Text})

		resp := AnswerResponse{Final: best.Text, Candidates: cands, Cached: false, Mode: mode}
		cacheSet(key, resp, cacheTTL)
		_ = writeNDJSON(w, streamMsg{Type: "meta", Meta: resp})
		return
	}

	top := []Candidate{cands[scores[0].Idx]}
	if len(scores) > 1 {
		top = append(top, cands[scores[1].Idx])
	}

	// Stream the synthesis (real streaming)
	_ = writeNDJSON(w, streamMsg{Type: "status", Text: "synthesizing..."})

	synthP := synthPrompt(req.Prompt, top)

	var final strings.Builder
	merged, err := ollamaGenerateStream(ctx, judgeModel, synthP, func(delta string) error {
		final.WriteString(delta)
		return writeNDJSON(w, streamMsg{Type: "delta", Text: delta})
	})
	if err != nil || strings.TrimSpace(merged) == "" {
		// Fallback to best judged candidate
		best := cands[scores[0].Idx].Text
		_ = writeNDJSON(w, streamMsg{Type: "status", Text: "synth failed; fallback to best candidate"})
		_ = writeNDJSON(w, streamMsg{Type: "delta", Text: best})

		resp := AnswerResponse{Final: best, Candidates: cands, Cached: false, Mode: mode}
		cacheSet(key, resp, cacheTTL)
		_ = writeNDJSON(w, streamMsg{Type: "meta", Meta: resp})
		return
	}

	finalText := strings.TrimSpace(final.String())
	resp := AnswerResponse{Final: finalText, Candidates: cands, Cached: false, Mode: mode}
	cacheSet(key, resp, cacheTTL)
	_ = writeNDJSON(w, streamMsg{Type: "meta", Meta: resp})
}

func main() {
	http.HandleFunc("/answer", handleAnswer)
	http.HandleFunc("/answer/stream", handleAnswerStream)

	log.Println("Go backend listening on :8080 (expects Ollama on :11434)")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
