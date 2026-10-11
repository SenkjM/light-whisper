// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Thin HTTP client for the Go engine when running `pnpm dev` without Tauri
 * (PLAN 步骤 6). Talks to the Vite proxy (`/lw-engine` → 127.0.0.1:8741) so
 * the browser does not need CORS headers from lw-engine.
 */

export type GoConfigSnapshot = {
  schema_version: number;
  revision: string;
  values: Record<string, unknown>;
  apply: Record<string, string>;
};

export type GoJob = {
  id: string;
  state: string;
  priority: string;
  progress?: number;
  text?: string | null;
  error?: string;
};

function base(): string {
  return (import.meta.env.VITE_LW_ENGINE_PROXY as string | undefined) || "/lw-engine";
}

function token(): string {
  const t =
    (import.meta.env.VITE_LW_ENGINE_TOKEN as string | undefined) ||
    (typeof sessionStorage !== "undefined"
      ? sessionStorage.getItem("lw-engine-token")
      : null) ||
    "";
  return t.trim();
}

export function setDevEngineToken(t: string) {
  sessionStorage.setItem("lw-engine-token", t.trim());
}

async function req(
  method: string,
  path: string,
  body?: BodyInit | null,
  contentType?: string,
): Promise<Response> {
  const headers: Record<string, string> = {
    Authorization: `Bearer ${token()}`,
  };
  if (contentType) headers["Content-Type"] = contentType;
  const res = await fetch(`${base()}${path}`, { method, headers, body: body ?? undefined });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`${method} ${path} → ${res.status}: ${text}`);
  }
  return res;
}

export async function goHealth(): Promise<{ api_version: number; status?: string }> {
  const res = await req("GET", "/health");
  return res.json();
}

export async function goStatus(): Promise<Record<string, unknown>> {
  const res = await req("GET", "/v1/engine/status");
  return res.json();
}

export async function goGetConfig(): Promise<GoConfigSnapshot> {
  const res = await req("GET", "/v1/config");
  return res.json();
}

export async function goPatchConfig(
  revision: string,
  values: Record<string, unknown>,
): Promise<GoConfigSnapshot & { applied?: string[]; pending_reload?: string[] }> {
  const res = await req(
    "PATCH",
    "/v1/config",
    JSON.stringify({ revision, values }),
    "application/json",
  );
  return res.json();
}

export async function goReload(): Promise<unknown> {
  const res = await req("POST", "/v1/engine/reload", "{}", "application/json");
  return res.json();
}

export async function goSubmitJobWav(
  wav: ArrayBuffer,
  priority = "normal",
): Promise<GoJob> {
  const q = new URLSearchParams({ priority, segments: "true" });
  const res = await req("POST", `/v1/jobs?${q}`, wav, "audio/wav");
  return res.json();
}

export async function goGetJob(id: string): Promise<GoJob> {
  const res = await req("GET", `/v1/jobs/${encodeURIComponent(id)}`);
  return res.json();
}

export async function goListJobs(): Promise<GoJob[]> {
  const res = await req("GET", "/v1/jobs");
  const data = await res.json();
  return data.jobs ?? data;
}

export async function goCancelJob(id: string): Promise<void> {
  await req("DELETE", `/v1/jobs/${encodeURIComponent(id)}`);
}
