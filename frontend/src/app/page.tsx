"use client";

import { useEffect, useMemo, useRef, useState } from "react";

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

function useTypewriter(text: string, opts?: { cps?: number; startDelayMs?: number }) {
    const cps = opts?.cps ?? 70; // characters per second
    const startDelayMs = opts?.startDelayMs ?? 120;

    const [out, setOut] = useState("");
    const [done, setDone] = useState(true);
    const timerRef = useRef<number | null>(null);
    const idxRef = useRef(0);

    useEffect(() => {
        // reset
        if (timerRef.current) window.clearInterval(timerRef.current);
        setOut("");
        setDone(false);
        idxRef.current = 0;

        if (!text) {
            setDone(true);
            return;
        }

        const msPerChar = Math.max(5, Math.floor(1000 / cps));

        const start = window.setTimeout(() => {
            timerRef.current = window.setInterval(() => {
                // burst mode: type a few chars per tick for smoothness
                const burst = 2;
                idxRef.current = Math.min(text.length, idxRef.current + burst);
                setOut(text.slice(0, idxRef.current));

                if (idxRef.current >= text.length) {
                    if (timerRef.current) window.clearInterval(timerRef.current);
                    timerRef.current = null;
                    setDone(true);
                }
            }, msPerChar);
        }, startDelayMs);

        return () => {
            window.clearTimeout(start);
            if (timerRef.current) window.clearInterval(timerRef.current);
            timerRef.current = null;
        };
    }, [text, cps, startDelayMs]);

    return { out, done, setOut };
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

    const { out: typedFinal, done: typingDone, setOut: forceTypedOut } = useTypewriter(finalAnswer, {
        cps: mode === "fast" ? 90 : 70,
        startDelayMs: 120,
    });

    // Auto-scroll the output panel as it types
    const outputRef = useRef<HTMLDivElement | null>(null);
    useEffect(() => {
        if (!outputRef.current) return;
        outputRef.current.scrollTop = outputRef.current.scrollHeight;
    }, [typedFinal]);

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

            setCached(Boolean(data.cached));
            setCandidates(data.candidates ?? []);
            setFinalAnswer(data.final ?? "");
        } catch (e: any) {
            setErr(e?.message ?? "Unknown error");
        } finally {
            setLoading(false);
        }
    }

    function skipTyping() {
        // Instantly show full answer
        forceTypedOut(finalAnswer);
    }

    return (
        <main className="min-h-screen bg-zinc-950 text-zinc-100">
            {/* subtle background glow */}
            <div className="pointer-events-none fixed inset-0 opacity-30">
                <div className="absolute -top-24 left-1/2 h-72 w-[42rem] -translate-x-1/2 rounded-full bg-emerald-500 blur-3xl" />
                <div className="absolute top-40 left-1/4 h-72 w-72 rounded-full bg-sky-500 blur-3xl" />
                <div className="absolute top-56 right-1/4 h-72 w-72 rounded-full bg-fuchsia-500 blur-3xl" />
            </div>

            <div className="relative mx-auto max-w-5xl px-4 py-10">
                {/* Header */}
                <div className="flex flex-col gap-2">
                    <div className="inline-flex flex-wrap items-center gap-2">
            <span className="rounded-lg bg-zinc-900/70 px-2 py-1 text-xs font-semibold text-emerald-300 ring-1 ring-zinc-800 backdrop-blur">
              local • ensemble
            </span>
                        {cached && (
                            <span className="rounded-lg bg-zinc-900/70 px-2 py-1 text-xs font-semibold text-sky-300 ring-1 ring-zinc-800 backdrop-blur">
                cache hit
              </span>
                        )}
                        <span className="rounded-lg bg-zinc-900/70 px-2 py-1 font-mono text-xs text-zinc-400 ring-1 ring-zinc-800 backdrop-blur">
              mode:{mode}
            </span>
                    </div>

                    <h1 className="text-3xl font-bold tracking-tight">
            <span className="bg-gradient-to-r from-emerald-300 via-sky-300 to-fuchsia-300 bg-clip-text text-transparent">
              project-llm
            </span>
                    </h1>

                    <p className="max-w-2xl text-sm text-zinc-400">
                        Fast mode: 2 models + shortcuts. Quality mode: 3 models + judge + synthesis.
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

                {/* Final Answer (terminal style + typing) */}
                {(finalAnswer || loading) && (
                    <div className="mt-8 overflow-hidden rounded-2xl ring-1 ring-zinc-800">
                        {/* terminal top bar */}
                        <div className="flex items-center justify-between bg-zinc-900/80 px-4 py-3 backdrop-blur">
                            <div className="flex items-center gap-2">
                                <span className="h-3 w-3 rounded-full bg-red-400/80" />
                                <span className="h-3 w-3 rounded-full bg-yellow-300/80" />
                                <span className="h-3 w-3 rounded-full bg-emerald-400/80" />
                                <span className="ml-3 font-mono text-xs text-zinc-400">stdout — /answer</span>
                            </div>

                            <div className="flex items-center gap-2">
                                {!typingDone && finalAnswer && (
                                    <button
                                        onClick={skipTyping}
                                        className="rounded-lg bg-zinc-950 px-2 py-1 text-xs font-semibold text-zinc-300 ring-1 ring-zinc-800 hover:bg-zinc-900"
                                    >
                                        Skip
                                    </button>
                                )}
                                <span className="rounded-lg bg-zinc-950 px-2 py-1 font-mono text-xs text-zinc-400 ring-1 ring-zinc-800">
                  {cached ? "cached" : "live"}
                </span>
                            </div>
                        </div>

                        {/* terminal body */}
                        <div
                            ref={outputRef}
                            className="max-h-[52vh] overflow-auto bg-zinc-950 px-4 py-4 font-mono text-sm leading-relaxed text-zinc-100"
                        >
                            {/* prompt echo */}
                            <div className="text-zinc-400">
                                <span className="text-emerald-300">user</span>
                                <span className="text-zinc-600">@</span>
                                <span className="text-sky-300">localhost</span>
                                <span className="text-zinc-600">:</span>
                                <span className="text-fuchsia-300">~</span>
                                <span className="text-zinc-600">$</span>{" "}
                                <span className="text-zinc-200">ask</span>{" "}
                                <span className="text-zinc-500">--mode</span>{" "}
                                <span className="text-zinc-200">{mode}</span>
                            </div>

                            <div className="mt-2 whitespace-pre-wrap">
                                {prompt.trim() || (loading ? "…" : "")}
                            </div>

                            <div className="mt-4 text-zinc-400">
                                <span className="text-emerald-300">assistant</span>
                                <span className="text-zinc-600">@</span>
                                <span className="text-sky-300">localhost</span>
                                <span className="text-zinc-600">:</span>
                                <span className="text-fuchsia-300">~</span>
                                <span className="text-zinc-600">$</span>{" "}
                                <span className="text-zinc-200">output</span>
                            </div>

                            <div className="mt-2 whitespace-pre-wrap">
                                {loading && !finalAnswer ? (
                                    <>
                                        <span className="text-zinc-300">Running models…</span>
                                        <span className="ml-2 inline-block animate-pulse text-emerald-300">█</span>
                                    </>
                                ) : (
                                    <>
                                        {typedFinal}
                                        <span
                                            className={`inline-block align-baseline ${
                                                typingDone ? "animate-pulse" : "animate-pulse"
                                            } text-emerald-300`}
                                        >
                      █
                    </span>
                                    </>
                                )}
                            </div>
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
                    Go + Next.js • local models via Ollama • terminal UI + typewriter
                </footer>
            </div>
        </main>
    );
}

