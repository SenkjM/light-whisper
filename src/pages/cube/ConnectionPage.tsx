// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  getManagementConnection,
  getManagementAuthMe,
  getManagementStats,
  setManagementSettings,
  hasAiPolishApiKey,
  saveAiPolishApiKey,
  hasAssistantApiKey,
  setAssistantApiKey,
  hasJevApiKey,
  setJevApiKey,
  setOnlineAsrApiKey,
  hasOnlineAsrApiKey,
  type ManagementConnection,
  type ManagementAuthMe,
  type ManagementStats,
} from "@/api/tauri";
import { loadAppPreferences } from "@/lib/appPreferences";

export default function ConnectionPage() {
  const { t } = useTranslation();
  const [conn, setConn] = useState<ManagementConnection | null>(null);
  const [me, setMe] = useState<ManagementAuthMe | null>(null);
  const [stats, setStats] = useState<ManagementStats | null>(null);
  const [urlDraft, setUrlDraft] = useState("");
  const [pathDraft, setPathDraft] = useState("");
  const [polishEp, setPolishEp] = useState("");
  const [assistantEp, setAssistantEp] = useState("");
  const [decisionEp, setDecisionEp] = useState("");
  const [asrEp, setAsrEp] = useState("");
  const [polishKey, setPolishKey] = useState("");
  const [assistantKey, setAssistantKey] = useState("");
  const [decisionKey, setDecisionKey] = useState("");
  const [asrKey, setAsrKey] = useState("");
  const [hasPolish, setHasPolish] = useState(false);
  const [hasAssistant, setHasAssistant] = useState(false);
  const [hasDecision, setHasDecision] = useState(false);
  const [hasAsr, setHasAsr] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const [c, prefs] = await Promise.all([
        getManagementConnection(),
        loadAppPreferences(),
      ]);
      setConn(c);
      setUrlDraft(c.baseUrl ?? "");
      setPathDraft(c.enginePath ?? "");
      setPolishEp(prefs.cloudPolishEndpoint ?? "");
      setAssistantEp(prefs.cloudAssistantEndpoint ?? "");
      setDecisionEp(prefs.cloudDecisionEndpoint ?? "");
      setAsrEp(prefs.cloudAsrEndpoint ?? "");
      setError(null);

      const keyChecks = Promise.all([
        hasAiPolishApiKey().catch(() => false),
        hasAssistantApiKey().catch(() => false),
        hasJevApiKey("liquid").catch(() => false),
        hasOnlineAsrApiKey().catch(() => false),
      ]);

      if (c.ready) {
        const [auth, st, keys] = await Promise.all([
          getManagementAuthMe().catch(() => null),
          getManagementStats().catch(() => null),
          keyChecks,
        ]);
        setMe(auth);
        setStats(st);
        const [polish, asst, jev, asr] = keys;
        setHasPolish(!!polish);
        setHasAssistant(!!asst);
        setHasDecision(!!jev);
        setHasAsr(!!asr);
      } else {
        setMe(null);
        setStats(null);
        const [polish, asst, jev, asr] = await keyChecks;
        setHasPolish(!!polish);
        setHasAssistant(!!asst);
        setHasDecision(!!jev);
        setHasAsr(!!asr);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    void refresh();
    const id = window.setInterval(() => void refresh(), 5000);
    return () => window.clearInterval(id);
  }, [refresh]);

  return (
    <div className="cube-page">
      <header className="cube-page-header">
        <h1>{t("cube.connectionTitle")}</h1>
        <p className="cube-muted">{t("cube.connectionHint")}</p>
      </header>

      <section className="cube-card">
        <h2>{t("cube.backendStatus")}</h2>
        {error && <p className="cube-error">{error}</p>}
        <dl className="cube-dl">
          <div><dt>{t("cube.backend")}</dt><dd>{conn?.backend ?? "—"}</dd></div>
          <div><dt>{t("cube.inferenceBackend")}</dt><dd className="mono">{conn?.inferenceBackend ?? "—"}</dd></div>
          <div><dt>{t("cube.nativeLibs")}</dt><dd>{conn?.nativeLibsPresent == null ? "—" : conn.nativeLibsPresent ? t("common.yes") : t("common.no")}</dd></div>
          <div><dt>{t("cube.ready")}</dt><dd>{conn?.ready ? t("common.yes") : t("common.no")}</dd></div>
          <div><dt>{t("cube.role")}</dt><dd>{me?.role ?? "—"}</dd></div>
          <div><dt>{t("cube.baseUrl")}</dt><dd className="mono">{conn?.baseUrl ?? "—"}</dd></div>
          <div><dt>{t("cube.packageFlavor")}</dt><dd>{conn?.packageFlavor ?? "—"}</dd></div>
          <div><dt>{t("cube.cudaR2t2")}</dt><dd>{conn?.cudaR2t2Present ? t("common.yes") : t("common.no")}</dd></div>
          <div>
            <dt>{t("cube.cudaTranscribe")}</dt>
            <dd>{conn?.cudaTranscribePresent ? t("common.yes") : t("common.no")}</dd>
          </div>
          <div>
            <dt>{t("cube.rootToken")}</dt>
            <dd className="mono">
              {conn?.autofillToken
                ? conn.rootToken ?? t("cube.tokenWaiting")
                : t("cube.tokenHidden")}
            </dd>
          </div>
        </dl>
        {me?.capabilities && (
          <p className="cube-muted" style={{ fontSize: 12 }}>
            {t("cube.capabilities")}: {me.capabilities.join(", ")}
          </p>
        )}
        <p className="cube-muted" style={{ fontSize: 12 }}>{t("cube.cudaRuntimeNote")}</p>
        {conn && !conn.cudaTranscribePresent ? (
          <p className="cube-muted" style={{ fontSize: 12 }}>{t("cube.cudaTranscribeHint")}</p>
        ) : null}
        {(conn?.inferenceBackend ?? "").toLowerCase() === "mock" ? (
          <p className="cube-error" role="alert">{t("cube.inferenceMockWarning")}</p>
        ) : null}
        {conn && !conn.cudaR2t2Present ? (
          <p className="cube-muted" style={{ fontSize: 12 }}>{t("cube.cudaLibsInstallHint")}</p>
        ) : null}
        <button type="button" onClick={() => void refresh()} disabled={busy}>
          {t("common.refresh")}
        </button>
      </section>

      <section className="cube-card">
        <h2>{t("cube.stats")}</h2>
        {stats ? (
          <dl className="cube-dl">
            <div><dt>HTTP</dt><dd>{stats.http_requests}</dd></div>
            <div><dt>ASR streams</dt><dd>{stats.asr_streams}</dd></div>
            <div><dt>Transcribes</dt><dd>{stats.transcribes}</dd></div>
            <div><dt>Jobs ok/fail</dt><dd>{stats.jobs_completed}/{stats.jobs_failed}</dd></div>
            <div><dt>Unauthorized</dt><dd>{stats.unauthorized}</dd></div>
          </dl>
        ) : (
          <p className="cube-muted">{conn?.ready ? t("cube.statsUnavailable") : t("cube.tokenWaiting")}</p>
        )}
      </section>

      <section className="cube-card">
        <h2>{t("cube.backendSettings")}</h2>
        <label className="cube-field">
          {t("cube.managementUrl")}
          <input value={urlDraft} onChange={(e) => setUrlDraft(e.target.value)} placeholder="http://127.0.0.1:…" />
        </label>
        <label className="cube-field">
          {t("cube.enginePath")}
          <input value={pathDraft} onChange={(e) => setPathDraft(e.target.value)} placeholder="lw-engine path override" />
        </label>
        <label className="cube-check">
          <input
            type="checkbox"
            checked={conn?.autofillToken ?? true}
            onChange={async (e) => {
              setBusy(true);
              try {
                await setManagementSettings({ managementAutofillToken: e.target.checked });
                await refresh();
              } finally {
                setBusy(false);
              }
            }}
          />
          {t("cube.autofillToken")}
        </label>
        <button
          type="button"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try {
              await setManagementSettings({
                managementUrl: urlDraft,
                managementEnginePath: pathDraft,
              });
              await refresh();
            } finally {
              setBusy(false);
            }
          }}
        >
          {t("common.save")}
        </button>
      </section>

      <section className="cube-card">
        <h2>{t("cube.cloudApisTitle")}</h2>
        <p className="cube-muted">{t("cube.cloudApisHint")}</p>

        <h3 className="cube-h3">{t("cube.cloudPolish")}</h3>
        <label className="cube-field">
          {t("cube.endpoint")}
          <input value={polishEp} onChange={(e) => setPolishEp(e.target.value)} placeholder="https://…" />
        </label>
        <label className="cube-field">
          {t("cube.apiKey")} ({hasPolish ? t("cube.keySet") : t("cube.keyEmpty")})
          <input type="password" value={polishKey} onChange={(e) => setPolishKey(e.target.value)} placeholder="••••" autoComplete="off" />
        </label>

        <h3 className="cube-h3">{t("cube.cloudAssistant")}</h3>
        <label className="cube-field">
          {t("cube.endpoint")}
          <input value={assistantEp} onChange={(e) => setAssistantEp(e.target.value)} placeholder="https://…" />
        </label>
        <label className="cube-field">
          {t("cube.apiKey")} ({hasAssistant ? t("cube.keySet") : t("cube.keyEmpty")})
          <input type="password" value={assistantKey} onChange={(e) => setAssistantKey(e.target.value)} placeholder="••••" autoComplete="off" />
        </label>

        <h3 className="cube-h3">{t("cube.cloudDecision")}</h3>
        <label className="cube-field">
          {t("cube.endpoint")}
          <input value={decisionEp} onChange={(e) => setDecisionEp(e.target.value)} placeholder="https://…" />
        </label>
        <label className="cube-field">
          {t("cube.apiKey")} ({hasDecision ? t("cube.keySet") : t("cube.keyEmpty")})
          <input type="password" value={decisionKey} onChange={(e) => setDecisionKey(e.target.value)} placeholder="••••" autoComplete="off" />
        </label>

        <h3 className="cube-h3">{t("cube.cloudAsr")}</h3>
        <p className="cube-muted">{t("cube.cloudAsrHint")}</p>
        <label className="cube-field">
          {t("cube.endpoint")}
          <input value={asrEp} onChange={(e) => setAsrEp(e.target.value)} placeholder="https://…" />
        </label>
        <label className="cube-field">
          {t("cube.apiKey")} ({hasAsr ? t("cube.keySet") : t("cube.keyEmpty")})
          <input type="password" value={asrKey} onChange={(e) => setAsrKey(e.target.value)} placeholder="••••" autoComplete="off" />
        </label>

        <button
          type="button"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try {
              await setManagementSettings({
                cloudPolishEndpoint: polishEp,
                cloudAssistantEndpoint: assistantEp,
                cloudDecisionEndpoint: decisionEp,
                cloudAsrEndpoint: asrEp,
              });
              if (polishKey.trim()) {
                await saveAiPolishApiKey(polishKey.trim());
                setPolishKey("");
              }
              if (assistantKey.trim()) {
                await setAssistantApiKey(assistantKey.trim());
                setAssistantKey("");
              }
              if (decisionKey.trim()) {
                await setJevApiKey("liquid", decisionKey.trim());
                setDecisionKey("");
              }
              if (asrKey.trim()) {
                await setOnlineAsrApiKey(asrKey.trim());
                setAsrKey("");
              }
              await refresh();
            } finally {
              setBusy(false);
            }
          }}
        >
          {t("common.save")}
        </button>
      </section>
    </div>
  );
}
