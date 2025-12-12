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

            const raw = await res.text(); // read as text first (handles errors)
            let data: any;

            try {
                data = JSON.parse(raw);
            } catch {
                throw new Error(`Non-JSON response (${res.status}): ${raw}`);
            }

            if (!res.ok) {
                throw new Error(data?.error ?? `Request failed (${res.status})`);
            }

            setAnswer(data.final ?? JSON.stringify(data, null, 2));
        } catch (err: any) {
            setAnswer(`Error: ${err?.message ?? "Unknown error"}`);
        } finally {
            setLoading(false);
        }
    }

    return (
        <main
            style={{
                maxWidth: 900,
                margin: "40px auto",
                padding: 16,
                fontFamily: "system-ui",
            }}
        >
            <h1 style={{ fontSize: 28, fontWeight: 700 }}>project-llm</h1>

            <textarea
                rows={6}
                style={{
                    width: "100%",
                    marginTop: 12,
                    padding: 10,
                    fontSize: 14,
                }}
                placeholder="Ask something..."
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
            />

            <button
                onClick={ask}
                disabled={loading || !prompt.trim()}
                style={{
                    marginTop: 12,
                    padding: "8px 16px",
                    fontSize: 14,
                    cursor: loading ? "not-allowed" : "pointer",
                }}
            >
                {loading ? "Thinking..." : "Ask"}
            </button>

            {answer && (
                <pre
                    style={{
                        marginTop: 20,
                        padding: 12,
                        background: "#f5f5f5",
                        whiteSpace: "pre-wrap",
                        borderRadius: 6,
                        fontSize: 14,
                    }}
                >
          {answer}
        </pre>
            )}
        </main>
    );
}
