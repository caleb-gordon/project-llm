"use client";

import { useState } from "react";

export default function Home() {
    const [prompt, setPrompt] = useState("");
    const [answer, setAnswer] = useState("");
    const [loading, setLoading] = useState(false);

    async function ask() {
        setLoading(true);
        setAnswer("");

        try {
            const res = await fetch("/api/answer", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ prompt }),
            });

            const raw = await res.text();
            const data = JSON.parse(raw);

            if (!res.ok) throw new Error(data?.error ?? `Request failed (${res.status})`);

            setAnswer(data.final ?? JSON.stringify(data, null, 2));
        } catch (e: any) {
            setAnswer(`Error: ${e?.message ?? "Unknown error"}`);
        } finally {
            setLoading(false);
        }
    }

    return (
        <main
            style={{
                minHeight: "100vh",
                background: "#ffffff",
                color: "#111111",
                padding: 16,
                fontFamily: "system-ui, -apple-system, Segoe UI, Roboto, Arial, sans-serif",
            }}
        >
            <div style={{ maxWidth: 900, margin: "40px auto" }}>
                <h1 style={{ fontSize: 28, fontWeight: 700, margin: 0 }}>project-llm</h1>
                <p style={{ marginTop: 8, color: "#444" }}>
                    Ask a question (local multi-LLM + judge + synthesis).
                </p>

                <textarea
                    rows={6}
                    value={prompt}
                    onChange={(e) => setPrompt(e.target.value)}
                    placeholder="Type a prompt..."
                    style={{
                        width: "100%",
                        marginTop: 12,
                        padding: 12,
                        border: "1px solid #ccc",
                        borderRadius: 8,
                        background: "#fff",
                        color: "#111",
                        outline: "none",
                        fontSize: 14,
                    }}
                />

                <button
                    onClick={ask}
                    disabled={loading || !prompt.trim()}
                    style={{
                        marginTop: 12,
                        padding: "10px 14px",
                        borderRadius: 8,
                        border: "1px solid #111",
                        background: loading || !prompt.trim() ? "#eee" : "#111",
                        color: loading || !prompt.trim() ? "#777" : "#fff",
                        cursor: loading || !prompt.trim() ? "not-allowed" : "pointer",
                        fontSize: 14,
                    }}
                >
                    {loading ? "Thinking..." : "Ask"}
                </button>

                {answer && (
                    <pre
                        style={{
                            marginTop: 16,
                            padding: 12,
                            borderRadius: 8,
                            border: "1px solid #ddd",
                            background: "#f7f7f7",
                            color: "#111",
                            whiteSpace: "pre-wrap",
                            lineHeight: 1.4,
                            fontSize: 14,
                        }}
                    >
            {answer}
          </pre>
                )}
            </div>
        </main>
    );
}
