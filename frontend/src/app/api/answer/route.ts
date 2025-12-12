import { NextResponse } from "next/server";

export async function POST(req: Request) {
    const body = await req.json();

    const upstream = await fetch("http://localhost:8080/answer", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
    });

    const text = await upstream.text();

    return new NextResponse(text, {
        status: upstream.status,
        headers: {
            "Content-Type": upstream.headers.get("Content-Type") ?? "application/json",
        },
    });
}
