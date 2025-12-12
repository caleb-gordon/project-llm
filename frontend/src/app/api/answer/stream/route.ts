export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function POST(req: Request) {
    const body = await req.json();

    const upstream = await fetch("http://localhost:8080/answer/stream", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
    });

    // Pass the stream through directly
    return new Response(upstream.body, {
        status: upstream.status,
        headers: {
            "Content-Type": upstream.headers.get("Content-Type") ?? "application/x-ndjson; charset=utf-8",
            "Cache-Control": "no-cache",
        },
    });
}
