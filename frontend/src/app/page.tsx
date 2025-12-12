"use client";

import { useMemo, useState } from "react";

type Candidate = {
    provider: string;
    text: string;
    latency_ms: number;
};

type OkResp = {
    final: string;
    candidates: Candidate[];
    cached: boolean;
    mode: "fast" | "quality";
};

type ErrResp = { error: string };

function isErr(x: any): x is ErrResp {
    return typeof x?.error === "string";
}

export default function Home() {
    const [prompt, setPrompt] = useState("");
    const [mode, setMode] = useState<"fast" | "quality">("fast");

    const [finalAnswer, setFinalAnswer] = useState("");
    const [candidates, setCandidates] = useState<Candidate[]>([]);
    const [cached, setCached] = useState(false);

    const [loading, setLoading] = useState(false);
    const [err, setErr] = useState("");

    const canAsk = useMemo(() => prompt.trim().length > 0 && !loading, [prompt, loading]);

    async function ask() {
        setLoading(true);
        setErr("");
        setFinalAnswer("");
        setCandidates([]);
        setCached(false);

        try {
            const res = await fetch("/api/answer", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ prompt, mode }),
            });

            const raw = await res.text();
            let data: OkResp | ErrResp;
            try {
                data = JSON.parse(raw);
            } catch {
                throw new Error(`Non-JSON response (${res.status}): ${raw}`);
            }

            if (!res.ok || isErr(data)) {
                throw new Error(isErr(data) ? data.error : `Request failed (${res.status})`);
            }

            setFinalAnswer(data.final);
            setCandidates(data.candidates ?? []);
            setCached(Boolean(data.cached));
        } catch (e: any) {
            setErr(e?.message ?? "Unknown error");
        } finally {
            setLoading(false);
        }
    }

    return (
        <main className="min-h-screen bg-zinc-950 text-zinc-100">
            <div className="mx-auto max-w-5xl px-4 py-10">
                {/* Header */}
                <div className="flex flex-col gap-2">
                    <div className="inline-flex items-center gap-2">
            <span className="rounded-lg bg-zinc-900 px-2 py-1 text-xs font-semibold text-emerald-300 ring-1 ring-zinc-800">
              local • ensemble
            </span>
                        {cached && (
                            <span className="rounded-lg bg-zinc-900 px-2 py-1 text-xs font-semibold text-sky-300 ring-1 ring-zinc-800">
                cache hit
              </span>
                        )}
                        <span className="rounded-lg bg-zinc-900 px-2 py-1 font-mono text-xs text-zinc-400 ring-1 ring-zinc-800">
              mode:{mode}
            </span>
                    </div>

                    <h1 className="text-3xl font-bold tracking-tight">
            <span className="bg-gradient-to-r from-emerald-300 via-sky-300 to-fuchsia-300 bg-clip-text text-transparent">
              project-llm
            </span>
                    </h1>

                    <p className="max-w-2xl text-sm text-zinc-400">
                        Fast mode: 2 models + smart shortcuts. Quality mode: 3 models + judge + synthesis.
                    </p>
                </div>

                {/* Prompt / Controls */}
                <div className="mt-8 rounded-2xl bg-zinc-900/60 p-4 ring-1 ring-zinc-800 backdrop-blur">
                    <div className="flex flex-col gap-4">
                        <div className="flex flex-wrap items-center justify-between gap-3">
                            <div className="inline-flex rounded-xl bg-zinc-950 ring-1 ring-zinc-800">
                                <button
                                    type="button"
                                    onClick={() => setMode("fast")}
                                    className={`px-4 py-2 text-sm font-semibold rounded-xl transition ${
                                        mode === "fast"
                                            ? "bg-emerald-400 text-zinc-950"
                                            : "text-zinc-300 hover:bg-zinc-900"
                                    }`}
                                >
                                    Fast
                                </button>
                                <button
                                    type="button"
                                    onClick={() => setMode("quality")}
                                    className={`px-4 py-2 text-sm font-semibold rounded-xl transition ${
                                        mode === "quality"
                                            ? "bg-fuchsia-300 text-zinc-950"
                                            : "text-zinc-300 hover:bg-zinc-900"
                                    }`}
                                >
                                    Quality
                                </button>
                            </div>

                            <button
                                onClick={ask}
                                disabled={!canAsk}
                                className="inline-flex items-center gap-2 rounded-xl bg-emerald-400 px-4 py-2 text-sm font-semibold text-zinc-950 transition disabled:cursor-not-allowed disabled:bg-zinc-800 disabled:text-zinc-500"
                            >
                                {loading ? (
                                    <>
                                        <span className="h-4 w-4 animate-spin rounded-full border-2 border-zinc-950 border-t-transparent" />
                                        Thinking…
                                    </>
                                ) : (
                                    <>
                                        <span className="font-mono">↵</span> Ask
                                    </>
                                )}
                            </button>
                        </div>

                        <textarea
                            className="w-full resize-none rounded-xl border border-zinc-800 bg-zinc-950 px-3 py-3 font-mono text-sm text-zinc-100 placeholder:text-zinc-600 focus:outline-none focus:ring-2 focus:ring-emerald-400/40"
                            rows={7}
                            placeholder="e.g. Explain quadratic probing vs chaining and when to use each."
                            value={prompt}
                            onChange={(e) => setPrompt(e.target.value)}
                        />

                        {err && (
                            <div className="rounded-xl border border-red-900/60 bg-red-950/30 p-3 text-sm text-red-200">
                                {err}
                            </div>
                        )}
                    </div>
                </div>

                {/* Final Answer */}
                {finalAnswer && (
                    <div className="mt-8 rounded-2xl bg-zinc-900/60 p-5 ring-1 ring-zinc-800 backdrop-blur">
                        <div className="flex items-center justify-between">
                            <h2 className="text-sm font-semibold text-zinc-200">Final answer</h2>
                            <span className="rounded-lg bg-zinc-950 px-2 py-1 font-mono text-xs text-zinc-400 ring-1 ring-zinc-800">
                /answer
              </span>
                        </div>
                        <div className="mt-3 whitespace-pre-wrap text-sm leading-relaxed text-zinc-100">
                            {finalAnswer}
                        </div>
                    </div>
                )}

                {/* Candidates */}
                {candidates.length > 0 && (
                    <div className="mt-8">
                        <div className="mb-3 flex items-center justify-between">
                            <h2 className="text-sm font-semibold text-zinc-200">Model candidates</h2>
                            <span className="text-xs text-zinc-500">{candidates.length} responses</span>
                        </div>

                        <div className="grid gap-4 md:grid-cols-3">
                            {candidates.map((c) => (
                                <div
                                    key={c.provider}
                                    className="rounded-2xl bg-zinc-900/60 p-4 ring-1 ring-zinc-800 backdrop-blur"
                                >
                                    <div className="flex items-center justify-between">
                    <span className="rounded-lg bg-zinc-950 px-2 py-1 text-xs font-semibold text-zinc-200 ring-1 ring-zinc-800">
                      {c.provider}
                    </span>
                                        <span className="font-mono text-xs text-zinc-500">{c.latency_ms}ms</span>
                                    </div>
                                    <div className="mt-3 line-clamp-[14] whitespace-pre-wrap font-mono text-xs text-zinc-300">
                                        {c.text}
                                    </div>
                                </div>
                            ))}
                        </div>
                    </div>
                )}

                <footer className="mt-12 text-center text-xs text-zinc-600">
                    Go + Next.js • local models via Ollama • fast/quality toggle
                </footer>
            </div>
        </main>
    );
}
