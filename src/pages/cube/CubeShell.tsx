// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

/**
 * 命令方块 main shell: left main tabs + content (PLAN §11.2).
 */

import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Cable,
  LayoutDashboard,
  Server,
  Settings,
  Info,
  Phone,
} from "lucide-react";
import ConnectionPage from "./ConnectionPage";
import WorkbenchPage, { type WorkbenchSub } from "./WorkbenchPage";
import BackendAdminPage from "./BackendAdminPage";
import ShellSettingsPage from "./ShellSettingsPage";
import AboutPage from "./AboutPage";

export type CubeTab = "connection" | "workbench" | "backend" | "shell" | "about";

const TABS: { id: CubeTab; icon: typeof Cable; labelKey: string }[] = [
  { id: "connection", icon: Cable, labelKey: "cube.tabConnection" },
  { id: "workbench", icon: LayoutDashboard, labelKey: "cube.tabWorkbench" },
  { id: "backend", icon: Server, labelKey: "cube.tabBackend" },
  { id: "shell", icon: Settings, labelKey: "cube.tabShell" },
  { id: "about", icon: Info, labelKey: "cube.tabAbout" },
];

export default function CubeShell({ animClass = "" }: { animClass?: string }) {
  const { t } = useTranslation();
  const [tab, setTab] = useState<CubeTab>("workbench");
  const [workbenchSub, setWorkbenchSub] = useState<WorkbenchSub>("dictation");

  const openShell = useCallback(() => setTab("shell"), []);

  return (
    <div className={`cube-shell ${animClass}`.trim()}>
      <aside className="cube-nav" aria-label={t("cube.navLabel")}>
        <div className="cube-brand">
          <Phone className="cube-brand-icon" strokeWidth={2.25} color="#e11d48" aria-hidden />
          <div>
            <div className="cube-brand-title">{t("cube.productName")}</div>
            <div className="cube-brand-sub">command-cube</div>
          </div>
        </div>
        <nav className="cube-nav-list">
          {TABS.map(({ id, icon: Icon, labelKey }) => (
            <button
              key={id}
              type="button"
              className={`cube-nav-item${tab === id ? " is-active" : ""}`}
              onClick={() => setTab(id)}
            >
              <Icon size={18} aria-hidden />
              <span>{t(labelKey)}</span>
            </button>
          ))}
        </nav>
      </aside>
      <main className="cube-main">
        {tab === "connection" && <ConnectionPage />}
        {tab === "workbench" && (
          <WorkbenchPage
            sub={workbenchSub}
            onSubChange={setWorkbenchSub}
            onOpenShell={openShell}
          />
        )}
        {tab === "backend" && <BackendAdminPage />}
        {tab === "shell" && <ShellSettingsPage />}
        {tab === "about" && <AboutPage />}
      </main>
    </div>
  );
}
