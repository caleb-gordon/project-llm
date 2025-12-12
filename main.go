package main

import (
	"encoding/json"
	"log"
	"net/http"
)

type AnswerRequest struct {
	Prompt string `json:"prompt"`
}

type AnswerResponse struct {
	Final  string `json:"final"`
	Cached bool   `json:"cached"`
}

func main() {
	http.HandleFunc("/answer", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		var req AnswerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AnswerResponse{
			Final:  "Backend connected. You asked: " + req.Prompt,
			Cached: false,
		})
	})

	log.Println("Go backend listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
