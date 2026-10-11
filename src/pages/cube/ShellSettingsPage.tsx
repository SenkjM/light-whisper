// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useTranslation } from "react-i18next";

export default function ShellSettingsPage() {
  const { t } = useTranslation();
  return (
    <div className="cube-page">
      <header className="cube-page-header">
        <h1>{t("cube.shellSettingsTitle")}</h1>
        <p className="cube-muted">{t("cube.shellSettingsHint")}</p>
      </header>
      <section className="cube-card">
        <p className="cube-muted">{t("cube.stubLater")}</p>
      </section>
    </div>
  );
}
