import type { ReactNode } from 'react';
import Link from 'next/link';
import LogoMark from '@/components/LogoMark';
import styles from './legal-page.module.css';

export type LegalSlug = 'privacy' | 'terms' | 'security' | 'data-processing';

export interface LegalSection {
  id: string;
  title: string;
  summary?: string;
  content: ReactNode;
}

interface LegalPageProps {
  slug: LegalSlug;
  eyebrow: string;
  title: string;
  description: string;
  effectiveDate: string;
  version: string;
  notice: ReactNode;
  sections: LegalSection[];
}

const documents: Array<{ slug: LegalSlug; label: string; short: string }> = [
  { slug: 'privacy', label: 'Privacy Policy', short: 'Privacy' },
  { slug: 'terms', label: 'Terms of Use', short: 'Terms' },
  { slug: 'security', label: 'Security & Trust', short: 'Security' },
  { slug: 'data-processing', label: 'Data Processing', short: 'Data processing' },
];

export function PolicyTable({
  headers,
  rows,
}: {
  headers: string[];
  rows: ReactNode[][];
}) {
  return (
    <div className={styles.tableScroll} tabIndex={0} role="region" aria-label={`${headers[0]} details`}>
      <table className={styles.policyTable}>
        <thead>
          <tr>{headers.map(header => <th scope="col" key={header}>{header}</th>)}</tr>
        </thead>
        <tbody>
          {rows.map((row, rowIndex) => (
            <tr key={rowIndex}>
              {row.map((cell, cellIndex) => cellIndex === 0
                ? <th scope="row" key={cellIndex}>{cell}</th>
                : <td key={cellIndex}>{cell}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function PolicyCallout({ title, children }: { title: string; children: ReactNode }) {
  return (
    <aside className={styles.callout} aria-label={title}>
      <span className={styles.calloutSignal} aria-hidden="true" />
      <div><strong>{title}</strong><p>{children}</p></div>
    </aside>
  );
}

export default function LegalPage({
  slug,
  eyebrow,
  title,
  description,
  effectiveDate,
  version,
  notice,
  sections,
}: LegalPageProps) {
  return (
    <div className={styles.shell}>
      <a className={styles.skipLink} href="#legal-content">Skip to document</a>

      <header className={styles.header}>
        <Link className={styles.brand} href="/" aria-label="DevicePulse sign in">
          <span className={styles.brandMark}><LogoMark /></span>
          <span><strong>DevicePulse</strong><small>Trust center</small></span>
        </Link>
        <nav className={styles.headerNav} aria-label="Primary">
          <Link href="/platform">Platform</Link>
          <Link className={styles.signInLink} href="/">Sign in <span aria-hidden="true">→</span></Link>
        </nav>
      </header>

      <main id="legal-content">
        <section className={styles.hero} aria-labelledby="document-title">
          <div className={styles.heroGrid} aria-hidden="true" />
          <div className={styles.heroInner}>
            <div className={styles.eyebrow}><i /> {eyebrow}</div>
            <h1 id="document-title">{title}</h1>
            <p>{description}</p>
            <dl className={styles.documentMeta}>
              <div><dt>Effective</dt><dd><time dateTime="2026-09-10">{effectiveDate}</time></dd></div>
              <div><dt>Version</dt><dd>{version}</dd></div>
              <div><dt>Scope</dt><dd>DevicePulse deployments</dd></div>
            </dl>
          </div>
        </section>

        <div className={styles.documentLayout}>
          <aside className={styles.rail}>
            <nav aria-label="Policy documents" className={styles.documentNav}>
              <span>Trust documents</span>
              {documents.map(document => (
                <Link
                  href={`/${document.slug}`}
                  key={document.slug}
                  aria-current={document.slug === slug ? 'page' : undefined}
                  className={document.slug === slug ? styles.activeDocument : undefined}
                >
                  <i aria-hidden="true" />{document.short}
                </Link>
              ))}
            </nav>
            <nav aria-label={`${title} contents`} className={styles.contentsNav}>
              <span>On this page</span>
              {sections.map((section, index) => (
                <a href={`#${section.id}`} key={section.id}>
                  <small>{String(index + 1).padStart(2, '0')}</small>{section.title}
                </a>
              ))}
            </nav>
          </aside>

          <article className={styles.article}>
            <div className={styles.deploymentNotice}>
              <span>Deployment note</span>
              <p>{notice}</p>
            </div>

            {sections.map((section, index) => (
              <section id={section.id} className={styles.policySection} key={section.id}>
                <div className={styles.sectionIndex} aria-hidden="true">{String(index + 1).padStart(2, '0')}</div>
                <div className={styles.sectionBody}>
                  <h2>{section.title}</h2>
                  {section.summary && <p className={styles.sectionSummary}>{section.summary}</p>}
                  <div className={styles.prose}>{section.content}</div>
                </div>
              </section>
            ))}
          </article>
        </div>
      </main>

      <footer className={styles.footer}>
        <div className={styles.footerBrand}><LogoMark /><span>DevicePulse endpoint operations</span></div>
        <nav aria-label="Legal">
          {documents.map(document => <Link href={`/${document.slug}`} key={document.slug}>{document.short}</Link>)}
        </nav>
        <span>Operational clarity. Responsible telemetry.</span>
      </footer>
    </div>
  );
}
