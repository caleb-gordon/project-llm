"use client";
import { useState } from "react";

export default function Home() {
  const [prompt, setPrompt] = useState("");
  const [answer, setAnswer] = useState("");
  const [loading, setLoading] = useState(false);

  async function ask() {
    setLoading(true);
    setAnswer("");

    const res = await fetch("/api/answer", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ prompt }),
    });

    const data = await res.json();
    setAnswer(data.final ?? JSON.stringify(data, null, 2));
    setLoading(false);
  }

  return (
      <main style={{ maxWidth: 800, margin: "40px auto", padding: 16 }}>
        <h1 style={{ fontSize: 28, fontWeight: 700 }}>project-llm</h1>

        <textarea
            rows={6}
            style={{ width: "100%", marginTop: 12 }}
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="Type a prompt..."
        />

        <button
            style={{ marginTop: 12, padding: "8px 14px" }}
            onClick={ask}
            disabled={loading || !prompt.trim()}
        >
          {loading ? "Thinking..." : "Ask"}
        </button>

        {answer && <pre style={{ marginTop: 16, whiteSpace: "pre-wrap" }}>{answer}</pre>}
      </main>
  );
}
