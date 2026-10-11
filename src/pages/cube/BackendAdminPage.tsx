// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useTranslation } from "react-i18next";

export default function BackendAdminPage() {
  const { t } = useTranslation();
  return (
    <div className="cube-page">
      <header className="cube-page-header">
        <h1>{t("cube.backendAdminTitle")}</h1>
        <p className="cube-muted">{t("cube.backendAdminHint")}</p>
      </header>
      <section className="cube-card">
        <h2>{t("cube.tokens")}</h2>
        <p className="cube-muted">{t("cube.stubLater")}</p>
      </section>
      <section className="cube-card">
        <h2>{t("cube.stats")}</h2>
        <p className="cube-muted">{t("cube.stubLater")}</p>
      </section>
    </div>
  );
}
