// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useTranslation } from "react-i18next";

/** Dictation + history entry (detailed history remains frontend-only). */
export default function WorkbenchPage() {
  const { t } = useTranslation();
  return (
    <div className="cube-page">
      <header className="cube-page-header">
        <h1>{t("cube.workbenchTitle")}</h1>
        <p className="cube-muted">{t("cube.workbenchHint")}</p>
      </header>
      <section className="cube-card">
        <h2>{t("cube.dictation")}</h2>
        <p className="cube-muted">{t("cube.stubLater")}</p>
      </section>
      <section className="cube-card">
        <h2>{t("cube.history")}</h2>
        <p className="cube-muted">{t("cube.historyFrontendOnly")}</p>
      </section>
    </div>
  );
}
