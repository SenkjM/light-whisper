// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useTranslation } from "react-i18next";
import MainPage from "@/pages/MainPage";
import HistoryPage from "@/pages/HistoryPage";

export type WorkbenchSub = "dictation" | "history";

export default function WorkbenchPage({
  sub,
  onSubChange,
  onOpenShell,
}: {
  sub: WorkbenchSub;
  onSubChange: (s: WorkbenchSub) => void;
  onOpenShell: () => void;
}) {
  const { t } = useTranslation();

  const handleNavigate = (v: "main" | "settings" | "history" | "dev-engine") => {
    if (v === "history") onSubChange("history");
    else if (v === "settings") onOpenShell();
    else if (v === "main") onSubChange("dictation");
  };

  return (
    <div className="cube-page cube-workbench">
      <header className="cube-page-header cube-workbench-header">
        <div>
          <h1>{t("cube.workbenchTitle")}</h1>
          <p className="cube-muted">{t("cube.workbenchHint")}</p>
        </div>
        <div className="cube-subtabs" role="tablist" aria-label={t("cube.workbenchTitle")}>
          <button
            type="button"
            role="tab"
            aria-selected={sub === "dictation"}
            className={sub === "dictation" ? "is-active" : ""}
            onClick={() => onSubChange("dictation")}
          >
            {t("cube.dictation")}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={sub === "history"}
            className={sub === "history" ? "is-active" : ""}
            onClick={() => onSubChange("history")}
          >
            {t("cube.history")}
          </button>
        </div>
      </header>
      <div className="cube-workbench-body">
        {sub === "dictation" ? (
          <MainPage embedded onNavigate={handleNavigate} />
        ) : (
          <HistoryPage embedded onNavigate={handleNavigate} />
        )}
      </div>
    </div>
  );
}
