package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
)

var ErrRateLimited = errors.New("rate_limited")

type AnswerRequest struct {
	Prompt string `json:"prompt"`
}

type AnswerResponse struct {
	Final  string `json:"final"`
	Cached bool   `json:"cached"`
}

type errResp struct {
	Error string `json:"error"`
}

type responsesCreateReq struct {
	Model        string `json:"model"`
	Input        string `json:"input"`
	Instructions string `json:"instructions,omitempty"`
	// Optional: Reasoning control (supported by some models)
	Reasoning map[string]string `json:"reasoning,omitempty"`
}

type responsesCreateResp struct {
	// The Responses API returns output in multiple forms; docs/examples show `output_text` as a convenient field.
	OutputText string `json:"output_text"`
	// If OutputText is empty, we’ll also try to fall back to raw JSON for debugging.
}

func main() {
	_ = godotenv.Load() // loads .env if present; OK if missing in prod

	http.HandleFunc("/answer", handleAnswer)

	log.Println("Go backend listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func handleAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req AnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		writeJSON(w, 500, errResp{Error: "OPENAI_API_KEY missing (check .env)"})
		return
	}

	text, err := callOpenAIResponses(apiKey, req.Prompt)
	if err != nil {
		// 429 should map to 429 so the UI can display it clearly
		status := http.StatusBadGateway
		if errors.Is(err, ErrRateLimited) {
			status = http.StatusTooManyRequests
		}
		writeJSON(w, status, errResp{Error: err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(AnswerResponse{Final: text, Cached: false})
}

func callOpenAIResponses(apiKey, prompt string) (string, error) {
	payload := responsesCreateReq{
		Model:        "gpt-5",
		Input:        prompt,
		Instructions: "Answer clearly and helpfully.",
		Reasoning:    map[string]string{"effort": "low"},
	}

	b, _ := json.Marshal(payload)

	httpClient := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/responses", bytes.NewReader(b))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var raw map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&raw)

		if resp.StatusCode == 429 {
			return "", ErrRateLimited
		}

		return "", errors.New("openai non-2xx: " + resp.Status)
	}

	var out responsesCreateResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.OutputText == "" {
		return "", errors.New("empty output_text (unexpected response shape)")
	}

	return out.OutputText, nil
}
