// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Browser-only Go engine debug console (PLAN 步骤 6).
 * Open with `pnpm dev` then http://localhost:1422/?view=dev-engine
 * after `./scripts/dev_go_engine.sh`.
 */

import { useCallback, useEffect, useState } from "react";
import {
  goCancelJob,
  goGetConfig,
  goGetJob,
  goHealth,
  goListJobs,
  goPatchConfig,
  goReload,
  goStatus,
  goSubmitJobWav,
  setDevEngineToken,
  type GoConfigSnapshot,
  type GoJob,
} from "@/dev/goEngineHttp";

export default function DevEngineConsole() {
  const [tokenInput, setTokenInput] = useState(
    () => sessionStorage.getItem("lw-engine-token") || "",
  );
  const [log, setLog] = useState<string[]>([]);
  const [config, setConfig] = useState<GoConfigSnapshot | null>(null);
  const [status, setStatus] = useState<Record<string, unknown> | null>(null);
  const [jobs, setJobs] = useState<GoJob[]>([]);
  const [engineDraft, setEngineDraft] = useState("");
  const [busy, setBusy] = useState(false);

  const push = useCallback((msg: string) => {
    setLog((prev) => [`${new Date().toISOString().slice(11, 19)} ${msg}`, ...prev].slice(0, 80));
  }, []);

  const refresh = useCallback(async () => {
    setBusy(true);
    try {
      const [h, st, cfg, jobList] = await Promise.all([
        goHealth(),
        goStatus(),
        goGetConfig(),
        goListJobs().catch(() => [] as GoJob[]),
      ]);
      setStatus(st);
      setConfig(cfg);
      setJobs(jobList);
      setEngineDraft(String(cfg.values?.engine ?? ""));
      push(`health ok api_version=${h.api_version}; engine=${cfg.values?.engine}`);
    } catch (e) {
      push(`error: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy(false);
    }
  }, [push]);

  useEffect(() => {
    if (tokenInput) void refresh();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div style={{ padding: 24, maxWidth: 880, margin: "0 auto", fontFamily: "system-ui" }}>
      <h1 style={{ fontSize: 20, marginBottom: 8 }}>Go engine console</h1>
      <p style={{ color: "var(--color-text-secondary, #666)", marginBottom: 16 }}>
        PLAN 步骤 6 — debug engine settings and file jobs without the Tauri shell.
        Start the engine with <code>./scripts/dev_go_engine.sh</code>, paste the token,
        then use the controls below.
      </p>

      <label style={{ display: "block", marginBottom: 12 }}>
        Bearer token
        <input
          style={{ display: "block", width: "100%", marginTop: 4, padding: 8 }}
          value={tokenInput}
          onChange={(e) => setTokenInput(e.target.value)}
          placeholder="from token file or stdout"
        />
      </label>
      <div style={{ display: "flex", gap: 8, marginBottom: 16, flexWrap: "wrap" }}>
        <button
          type="button"
          disabled={busy}
          onClick={() => {
            setDevEngineToken(tokenInput);
            void refresh();
          }}
        >
          Save token & refresh
        </button>
        <button type="button" disabled={busy} onClick={() => void refresh()}>
          Refresh
        </button>
        <button
          type="button"
          disabled={busy || !config}
          onClick={async () => {
            if (!config) return;
            setBusy(true);
            try {
              const next = await goPatchConfig(config.revision, { engine: engineDraft });
              setConfig(next);
              push(`patched engine → ${engineDraft}`);
              if (next.pending_reload?.length) {
                await goReload();
                push("reload requested");
                await refresh();
              }
            } catch (e) {
              push(`patch error: ${e instanceof Error ? e.message : String(e)}`);
            } finally {
              setBusy(false);
            }
          }}
        >
          Apply engine
        </button>
        <button
          type="button"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try {
              await goReload();
              push("reload ok");
              await refresh();
            } catch (e) {
              push(`reload error: ${e instanceof Error ? e.message : String(e)}`);
            } finally {
              setBusy(false);
            }
          }}
        >
          Reload models
        </button>
      </div>

      <label style={{ display: "block", marginBottom: 16 }}>
        Engine id
        <input
          style={{ display: "block", width: "100%", marginTop: 4, padding: 8 }}
          value={engineDraft}
          onChange={(e) => setEngineDraft(e.target.value)}
          placeholder="qwen3-asr-0.6b | confucius4-r2t2 | …"
        />
      </label>

      <h2 style={{ fontSize: 16 }}>File job (WAV)</h2>
      <input
        type="file"
        accept="audio/wav,.wav"
        disabled={busy}
        onChange={async (e) => {
          const file = e.target.files?.[0];
          if (!file) return;
          setBusy(true);
          try {
            const buf = await file.arrayBuffer();
            const job = await goSubmitJobWav(buf, "normal");
            push(`job ${job.id} → ${job.state}`);
            for (let i = 0; i < 60; i++) {
              await new Promise((r) => setTimeout(r, 500));
              const j = await goGetJob(job.id);
              push(`job ${j.id} ${j.state} progress=${j.progress ?? 0}`);
              if (j.state === "completed" || j.state === "failed" || j.state === "cancelled") {
                if (j.text) push(`text: ${j.text.slice(0, 200)}`);
                break;
              }
            }
            setJobs(await goListJobs());
          } catch (err) {
            push(`job error: ${err instanceof Error ? err.message : String(err)}`);
          } finally {
            setBusy(false);
          }
        }}
      />

      {jobs.length > 0 && (
        <ul style={{ marginTop: 12 }}>
          {jobs.map((j) => (
            <li key={j.id}>
              {j.id.slice(0, 8)}… {j.state}{" "}
              <button
                type="button"
                onClick={async () => {
                  await goCancelJob(j.id);
                  push(`cancelled ${j.id}`);
                  setJobs(await goListJobs());
                }}
              >
                cancel
              </button>
            </li>
          ))}
        </ul>
      )}

      <h2 style={{ fontSize: 16, marginTop: 24 }}>Status</h2>
      <pre style={{ fontSize: 12, background: "#1111", padding: 12, overflow: "auto" }}>
        {JSON.stringify({ status, config }, null, 2)}
      </pre>

      <h2 style={{ fontSize: 16 }}>Log</h2>
      <pre style={{ fontSize: 12, background: "#1111", padding: 12, maxHeight: 240, overflow: "auto" }}>
        {log.join("\n")}
      </pre>
    </div>
  );
}
