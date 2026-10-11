// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  getManagementConnection,
  setManagementSettings,
  type ManagementConnection,
} from "@/api/tauri";

export default function ConnectionPage() {
  const { t } = useTranslation();
  const [conn, setConn] = useState<ManagementConnection | null>(null);
  const [urlDraft, setUrlDraft] = useState("");
  const [pathDraft, setPathDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const c = await getManagementConnection();
      setConn(c);
      setUrlDraft(c.baseUrl ?? "");
      setPathDraft(c.enginePath ?? "");
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    void refresh();
    const id = window.setInterval(() => void refresh(), 4000);
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
          <div><dt>{t("cube.ready")}</dt><dd>{conn?.ready ? t("common.yes") : t("common.no")}</dd></div>
          <div><dt>{t("cube.baseUrl")}</dt><dd className="mono">{conn?.baseUrl ?? "—"}</dd></div>
          <div><dt>{t("cube.packageFlavor")}</dt><dd>{conn?.packageFlavor ?? "—"}</dd></div>
          <div>
            <dt>{t("cube.rootToken")}</dt>
            <dd className="mono">
              {conn?.autofillToken
                ? conn.rootToken ?? t("cube.tokenWaiting")
                : t("cube.tokenHidden")}
            </dd>
          </div>
        </dl>
        <button type="button" onClick={() => void refresh()} disabled={busy}>
          {t("common.refresh")}
        </button>
      </section>

      <section className="cube-card">
        <h2>{t("cube.backendSettings")}</h2>
        <label className="cube-field">
          {t("cube.managementUrl")}
          <input
            value={urlDraft}
            onChange={(e) => setUrlDraft(e.target.value)}
            placeholder="http://127.0.0.1:… (empty = child process)"
          />
        </label>
        <label className="cube-field">
          {t("cube.enginePath")}
          <input
            value={pathDraft}
            onChange={(e) => setPathDraft(e.target.value)}
            placeholder="lw-engine path override"
          />
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
        <ul className="cube-stub-list">
          <li>{t("cube.cloudPolish")} — {t("cube.stubLater")}</li>
          <li>{t("cube.cloudAssistant")} — {t("cube.stubLater")}</li>
          <li>{t("cube.cloudDecision")} — {t("cube.stubLater")}</li>
        </ul>
      </section>
    </div>
  );
}
