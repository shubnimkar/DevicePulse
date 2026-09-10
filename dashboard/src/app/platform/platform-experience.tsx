'use client';

import { motion, useMotionValue, useReducedMotion, useSpring, useTransform } from 'motion/react';
import Link from 'next/link';
import type { PointerEvent } from 'react';
import LogoMark from '@/components/LogoMark';
import styles from './platform.module.css';

const capabilities = [
  {
    index: '01',
    title: 'Live endpoint health',
    copy: 'Track CPU, memory, disks, battery and network throughput from the latest device check-in.',
    signal: 'HEALTH',
  },
  {
    index: '02',
    title: 'Asset inventory',
    copy: 'Keep hardware, operating system and installed software context attached to every endpoint.',
    signal: 'INVENTORY',
  },
  {
    index: '03',
    title: 'Runtime visibility',
    copy: 'Inspect processes, system services and listening ports without changing operational context.',
    signal: 'RUNTIME',
  },
  {
    index: '04',
    title: 'Activity intelligence',
    copy: 'Understand active applications and browser activity with retention and collection controls.',
    signal: 'ACTIVITY',
  },
  {
    index: '05',
    title: 'Security operations',
    copy: 'Review OS updates and USB events, then route authorized remote actions through role controls.',
    signal: 'SECURITY',
  },
  {
    index: '06',
    title: 'Fleet reporting',
    copy: 'Turn endpoint telemetry into weekly operational summaries and focused on-demand reports.',
    signal: 'REPORTS',
  },
];

const telemetryRows = [
  ['Hardware health', 'CPU · memory · disk · battery'],
  ['Runtime state', 'Processes · services · open ports'],
  ['Endpoint context', 'OS · architecture · installed apps'],
  ['Activity signals', 'Active apps · browser history'],
  ['Security posture', 'Updates · USB events · quarantine'],
];

function ProductVisual() {
  const reduceMotion = useReducedMotion();
  const pointerX = useMotionValue(0);
  const pointerY = useMotionValue(0);
  const springX = useSpring(pointerX, { stiffness: 180, damping: 26, mass: .6 });
  const springY = useSpring(pointerY, { stiffness: 180, damping: 26, mass: .6 });
  const rotateX = useTransform(springY, [-.5, .5], [3.5, -3.5]);
  const rotateY = useTransform(springX, [-.5, .5], [-5, 5]);

  const move = (event: PointerEvent<HTMLDivElement>) => {
    if (reduceMotion || event.pointerType === 'touch') return;
    const bounds = event.currentTarget.getBoundingClientRect();
    pointerX.set((event.clientX - bounds.left) / bounds.width - .5);
    pointerY.set((event.clientY - bounds.top) / bounds.height - .5);
  };

  const reset = () => {
    pointerX.set(0);
    pointerY.set(0);
  };

  return (
    <motion.div
      className={styles.visualStage}
      onPointerMove={move}
      onPointerLeave={reset}
      initial={reduceMotion ? false : { opacity: 0, y: 24 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: .7, ease: [0.22, 1, 0.36, 1] }}
      style={{
        rotateX: reduceMotion ? 0 : rotateX,
        rotateY: reduceMotion ? 0 : rotateY,
        transformPerspective: 1200,
        transformStyle: 'preserve-3d',
      }}
      aria-label="DevicePulse console preview showing fleet health and endpoints requiring attention"
      role="img"
    >
      <div className={styles.visualGlow} aria-hidden="true" />
      <div className={styles.consoleFrame}>
        <div className={styles.consoleTopbar}>
          <span className={styles.consoleBrand}><i /> DevicePulse / Overview</span>
          <span className={styles.consoleLive}><i /> Live</span>
        </div>
        <div className={styles.consoleBody}>
          <div className={styles.consoleRail} aria-hidden="true">
            <i className={styles.railActive} /><i /><i /><i /><i />
          </div>
          <div className={styles.consoleContent}>
            <div className={styles.consoleHeading}><span>Fleet operations</span><b>Command overview</b></div>
            <div className={styles.consoleStats}>
              <div><span>Total</span><b>128</b><i /></div>
              <div className={styles.statOnline}><span>Online</span><b>119</b><i /></div>
              <div className={styles.statWarn}><span>Attention</span><b>04</b><i /></div>
            </div>
            <div className={styles.consoleWorkspace}>
              <div className={styles.consoleQueue}>
                <div className={styles.consoleSectionTitle}><span>Priority queue</span><b>Needs attention</b></div>
                {['Design workstation 08', 'Finance laptop 14', 'Build runner 03'].map((name, index) => (
                  <div className={styles.consoleDevice} key={name}>
                    <i className={index === 2 ? styles.deviceOffline : ''}>{index === 2 ? '–' : '!'}</i>
                    <span><b>{name}</b><small>{index === 2 ? 'Check-in overdue' : `${91 - index * 4}% peak resource use`}</small></span>
                    <em>{index === 2 ? 'offline' : 'review'}</em>
                  </div>
                ))}
              </div>
              <div className={styles.consoleRadar}>
                <div className={styles.radarRing}><i /><b>93%</b><span>connected</span></div>
                <small>119 / 128 reporting</small>
              </div>
            </div>
          </div>
        </div>
      </div>
      <div className={`${styles.floatChip} ${styles.floatChipTop}`} aria-hidden="true"><i /> encrypted response</div>
      <div className={`${styles.floatChip} ${styles.floatChipBottom}`} aria-hidden="true"><i /> 10s sync policy</div>
    </motion.div>
  );
}

export default function PlatformExperience() {
  const reduceMotion = useReducedMotion();

  return (
    <div className={styles.page}>
      <a className={styles.skipLink} href="#main-content">Skip to content</a>
      <header className={styles.header}>
        <Link className={styles.brand} href="/platform" aria-label="DevicePulse platform home">
          <span className={styles.logo}><LogoMark /></span>
          <span>DevicePulse<small>Endpoint operations</small></span>
        </Link>
        <nav className={styles.nav} aria-label="Platform navigation">
          <a href="#capabilities">Capabilities</a>
          <a href="#architecture">Architecture</a>
          <a href="#security">Security</a>
        </nav>
        <Link className={styles.consoleLink} href="/">Open console <span aria-hidden="true">↗</span></Link>
      </header>

      <main id="main-content">
        <section className={styles.hero}>
          <div className={styles.heroGrid} aria-hidden="true" />
          <motion.div
            className={styles.heroCopy}
            initial={reduceMotion ? false : { opacity: 0, y: 18 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: .6, ease: [0.22, 1, 0.36, 1] }}
          >
            <div className={styles.eyebrow}><i /> Endpoint telemetry platform</div>
            <h1>Endpoint monitoring<br /><span>without the blind spots.</span></h1>
            <p>
              DevicePulse brings live device health, endpoint inventory, runtime activity and security posture into one focused operations console.
            </p>
            <div className={styles.heroActions}>
              <a className={styles.primaryAction} href="#architecture">Explore the architecture <span aria-hidden="true">↓</span></a>
              <Link className={styles.secondaryAction} href="/">Sign in to the console</Link>
            </div>
            <ul className={styles.platformStrip} aria-label="Supported operating systems and delivery">
              <li><i /> Linux</li><li><i /> Windows</li><li><i /> macOS</li><li><i /> Offline queue</li>
            </ul>
          </motion.div>
          <ProductVisual />
        </section>

        <section className={styles.signalBand} aria-label="Platform characteristics">
          <div><span>01 / Signal</span><strong>Near-real-time visibility</strong><small>Policy-controlled collection and sync</small></div>
          <div><span>02 / Context</span><strong>One endpoint record</strong><small>Health, inventory and activity together</small></div>
          <div><span>03 / Continuity</span><strong>Built for unreliable links</strong><small>Local SQLite queue with batch recovery</small></div>
        </section>

        <section className={styles.section} id="capabilities">
          <div className={styles.sectionIntro}>
            <div className={styles.sectionIndex}>01 — Capabilities</div>
            <h2>One operational picture.<br />Every useful endpoint signal.</h2>
            <p>Move from fleet-level health to device-level evidence without stitching together disconnected monitoring screens.</p>
          </div>
          <div className={styles.capabilityGrid}>
            {capabilities.map((capability, index) => (
              <motion.article
                className={styles.capabilityCard}
                key={capability.title}
                initial={reduceMotion ? false : { opacity: 0, y: 16 }}
                whileInView={{ opacity: 1, y: 0 }}
                viewport={{ once: true, amount: .2 }}
                transition={{ delay: reduceMotion ? 0 : index * .04, duration: .4 }}
              >
                <div><span>{capability.index}</span><i>{capability.signal}</i></div>
                <h3>{capability.title}</h3>
                <p>{capability.copy}</p>
              </motion.article>
            ))}
          </div>
        </section>

        <section className={`${styles.section} ${styles.architectureSection}`} id="architecture">
          <div className={styles.sectionIntro}>
            <div className={styles.sectionIndex}>02 — Architecture</div>
            <h2>Lightweight at the edge.<br />Structured at the center.</h2>
            <p>A small Go agent collects configured signals, survives connectivity gaps and reports into a centralized API and dashboard.</p>
          </div>
          <ol className={styles.architectureFlow}>
            <li>
              <span>01</span><div className={styles.archIcon}>A</div>
              <h3>Endpoint agent</h3><p>Collects approved hardware, software, runtime and activity telemetry.</p>
              <small>Go · cross-platform</small>
            </li>
            <li>
              <span>02</span><div className={styles.archIcon}>Q</div>
              <h3>Durable queue</h3><p>Buffers telemetry locally when the network is unavailable, then resumes in batches.</p>
              <small>SQLite · offline-first</small>
            </li>
            <li>
              <span>03</span><div className={styles.archIcon}>↯</div>
              <h3>Control API</h3><p>Authenticates agents, applies policy and stores the latest fleet state.</p>
              <small>Go API · MongoDB</small>
            </li>
            <li>
              <span>04</span><div className={styles.archIcon}>D</div>
              <h3>Operations console</h3><p>Turns incoming telemetry into fleet health, investigation and reporting workflows.</p>
              <small>Next.js · encrypted responses</small>
            </li>
          </ol>
        </section>

        <section className={`${styles.section} ${styles.securitySection}`} id="security">
          <div className={styles.securityCopy}>
            <div className={styles.sectionIndex}>03 — Security model</div>
            <h2>Operational visibility with deliberate boundaries.</h2>
            <p>DevicePulse separates agent credentials from dashboard sessions, restricts privileged actions by role and encrypts dashboard API response bodies in transit.</p>
            <ul>
              <li><i /> HTTP-only dashboard session cookies</li>
              <li><i /> Admin, manager and viewer roles</li>
              <li><i /> Per-request ECDH key agreement and AES-GCM response encryption</li>
              <li><i /> SHA-256 verification before agent updates</li>
            </ul>
          </div>
          <div className={styles.telemetryTable}>
            <div className={styles.tableHeader}><span>Signal group</span><span>Example telemetry</span></div>
            {telemetryRows.map(([group, details]) => (
              <div className={styles.tableRow} key={group}><strong>{group}</strong><span>{details}</span></div>
            ))}
            <div className={styles.tableFoot}><i /> Collection groups can be governed from fleet policy.</div>
          </div>
        </section>

        <section className={styles.ctaSection}>
          <div className={styles.ctaGrid} aria-hidden="true" />
          <div className={styles.sectionIndex}>Start with signal</div>
          <h2>See the fleet as it is.<br /><span>Act with context.</span></h2>
          <p>Open the private DevicePulse operations console to inspect your connected endpoints.</p>
          <Link className={styles.primaryAction} href="/">Open DevicePulse console <span aria-hidden="true">↗</span></Link>
        </section>
      </main>

      <footer className={styles.footer}>
        <Link className={styles.brand} href="/platform">
          <span className={styles.logo}><LogoMark /></span><span>DevicePulse<small>Endpoint operations</small></span>
        </Link>
        <p>Cross-platform endpoint telemetry for modern IT operations.</p>
        <Link href="/">Console sign in</Link>
      </footer>
    </div>
  );
}
