// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  getManagementAuthMe,
  getManagementStats,
  listManagementTokens,
  createManagementToken,
  deleteManagementToken,
  type ManagementAuthMe,
  type ManagementStats,
  type ManagementTokenMeta,
} from "@/api/tauri";

export default function BackendAdminPage() {
  const { t } = useTranslation();
  const [me, setMe] = useState<ManagementAuthMe | null>(null);
  const [stats, setStats] = useState<ManagementStats | null>(null);
  const [tokens, setTokens] = useState<ManagementTokenMeta[]>([]);
  const [role, setRole] = useState<"admin" | "user">("user");
  const [label, setLabel] = useState("");
  const [createdPlain, setCreatedPlain] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const canManage = !!me?.capabilities?.includes("token.manage");
  const canList = !!me?.capabilities?.includes("token.list");

  const refresh = useCallback(async () => {
    try {
      const auth = await getManagementAuthMe();
      setMe(auth);
      const st = await getManagementStats().catch(() => null);
      setStats(st);
      if (auth.capabilities.includes("token.list")) {
        const list = await listManagementTokens();
        setTokens(list.tokens ?? []);
      } else {
        setTokens([]);
      }
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return (
    <div className="cube-page">
      <header className="cube-page-header">
        <h1>{t("cube.backendAdminTitle")}</h1>
        <p className="cube-muted">{t("cube.backendAdminHint")}</p>
      </header>

      {error && <p className="cube-error">{error}</p>}

      <section className="cube-card">
        <h2>{t("cube.role")}</h2>
        <dl className="cube-dl">
          <div><dt>ID</dt><dd className="mono">{me?.id ?? "—"}</dd></div>
          <div><dt>{t("cube.role")}</dt><dd>{me?.role ?? "—"}</dd></div>
        </dl>
        {me?.capabilities && (
          <ul className="cube-stub-list">
            {me.capabilities.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ul>
        )}
      </section>

      <section className="cube-card">
        <h2>{t("cube.stats")}</h2>
        {stats ? (
          <dl className="cube-dl">
            <div><dt>HTTP</dt><dd>{stats.http_requests}</dd></div>
            <div><dt>ASR streams</dt><dd>{stats.asr_streams}</dd></div>
            <div><dt>Transcribes</dt><dd>{stats.transcribes}</dd></div>
            <div><dt>Jobs submitted</dt><dd>{stats.jobs_submitted}</dd></div>
            <div><dt>Jobs completed</dt><dd>{stats.jobs_completed}</dd></div>
            <div><dt>Jobs failed</dt><dd>{stats.jobs_failed}</dd></div>
            <div><dt>Unauthorized</dt><dd>{stats.unauthorized}</dd></div>
          </dl>
        ) : (
          <p className="cube-muted">{t("cube.statsUnavailable")}</p>
        )}
        <button type="button" onClick={() => void refresh()}>{t("common.refresh")}</button>
      </section>

      <section className="cube-card">
        <h2>{t("cube.tokens")}</h2>
        {!canList && <p className="cube-muted">{t("cube.needAdmin")}</p>}
        {canList && (
          <table className="cube-table">
            <thead>
              <tr>
                <th>ID</th>
                <th>{t("cube.role")}</th>
                <th>{t("cube.tokenLabel")}</th>
                <th>{t("cube.tokenPrefix")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {tokens.map((tok) => (
                <tr key={tok.id}>
                  <td className="mono">{tok.id}</td>
                  <td>{tok.role}</td>
                  <td>{tok.label || "—"}</td>
                  <td className="mono">
                    {tok.role === "root" || tok.masked
                      ? t("cube.rootMasked")
                      : tok.prefix
                        ? `${tok.prefix}…`
                        : "—"}
                  </td>
                  <td>
                    {canManage && tok.role !== "root" && (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={async () => {
                          if (!window.confirm(t("cube.confirmDeleteToken"))) return;
                          setBusy(true);
                          try {
                            await deleteManagementToken(tok.id);
                            toast.success(t("cube.tokenDeleted"));
                            await refresh();
                          } catch (e) {
                            toast.error(e instanceof Error ? e.message : String(e));
                          } finally {
                            setBusy(false);
                          }
                        }}
                      >
                        {t("common.delete")}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

        {canManage && (
          <div className="cube-create-token">
            <h3 className="cube-h3">{t("cube.createToken")}</h3>
            <label className="cube-field">
              {t("cube.role")}
              <select className="cube-select" value={role} onChange={(e) => setRole(e.target.value as "admin" | "user")}>
                <option value="user">user</option>
                <option value="admin">admin</option>
              </select>
            </label>
            <label className="cube-field">
              {t("cube.tokenLabel")}
              <input value={label} onChange={(e) => setLabel(e.target.value)} />
            </label>
            <button
              type="button"
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                setCreatedPlain(null);
                try {
                  const created = await createManagementToken(role, label);
                  setCreatedPlain(created.token);
                  toast.success(t("cube.tokenCreatedOnce"));
                  setLabel("");
                  await refresh();
                } catch (e) {
                  toast.error(e instanceof Error ? e.message : String(e));
                } finally {
                  setBusy(false);
                }
              }}
            >
              {t("cube.createToken")}
            </button>
            {createdPlain && (
              <p className="mono cube-created-token">
                {t("cube.tokenPlainOnce")}: {createdPlain}
              </p>
            )}
          </div>
        )}
      </section>
    </div>
  );
}
