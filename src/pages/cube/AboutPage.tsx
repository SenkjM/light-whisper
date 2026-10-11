// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

import { useTranslation } from "react-i18next";
import { Phone } from "lucide-react";

export default function AboutPage() {
  const { t } = useTranslation();
  return (
    <div className="cube-page">
      <header className="cube-page-header">
        <h1>{t("cube.aboutTitle")}</h1>
      </header>
      <section className="cube-card cube-about">
        <Phone size={48} color="#e11d48" aria-hidden />
        <h2>{t("cube.productName")}</h2>
        <p className="cube-muted">command-cube</p>
        <p>{t("cube.aboutIconNote")}</p>
        <p className="cube-muted">{t("cube.aboutLicense")}</p>
      </section>
    </div>
  );
}
