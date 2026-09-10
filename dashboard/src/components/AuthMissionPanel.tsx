'use client';

import { motion, useReducedMotion } from 'motion/react';
import LogoMark from '@/components/LogoMark';

const signalRows = [
  { label: 'Endpoint health', value: 'Nominal', tone: 'ok' },
  { label: 'Policy channel', value: 'Secured', tone: 'blue' },
  { label: 'Telemetry queue', value: 'Synced', tone: 'cyan' },
];

export default function AuthMissionPanel() {
  const reduceMotion = useReducedMotion();

  return (
    <aside className="auth-brand" aria-label="About DevicePulse">
      <motion.div
        className="auth-brand-inner"
        initial={reduceMotion ? false : { opacity: 0 }}
        animate={{ opacity: 1 }}
        transition={{ duration: .55, ease: [0.22, 1, 0.36, 1] }}
      >
        <div className="auth-brand-top">
          <div className="auth-brand-mark"><LogoMark /></div>
          <div className="auth-brand-lockup">
            <strong>DevicePulse</strong>
            <span>Endpoint operations</span>
          </div>
        </div>

        <div className="auth-brand-copy">
          <div className="auth-eyebrow"><i aria-hidden="true" /> Secure command layer</div>
          <h2 className="auth-brand-headline">Every endpoint.<br /><span>One operational picture.</span></h2>
          <p className="auth-brand-sub">
            Move from fleet health to device-level evidence without losing context.
            Hardware, runtime, activity and security telemetry stay connected.
          </p>
        </div>

        <div className="auth-command-visual" aria-hidden="true">
          <div className="auth-orbit-stage">
            <motion.div
              className="auth-radar-sweep"
              animate={reduceMotion ? undefined : { rotate: 360 }}
              transition={{ duration: 8, ease: 'linear', repeat: Infinity }}
            />
            <div className="auth-orbit orbit-one" />
            <div className="auth-orbit orbit-two" />
            <div className="auth-orbit-core"><LogoMark /></div>
            <i className="auth-orbit-node node-one" />
            <i className="auth-orbit-node node-two" />
            <i className="auth-orbit-node node-three" />
            <span className="auth-orbit-label label-agent">Agents</span>
            <span className="auth-orbit-label label-api">Control API</span>
            <span className="auth-orbit-label label-console">Console</span>
          </div>
          <div className="auth-signal-stack">
            <div className="auth-signal-head"><span>Illustrative fleet signal</span><i /></div>
            {signalRows.map(row => (
              <div className="auth-signal-row" key={row.label}>
                <span><i className={`tone-${row.tone}`} />{row.label}</span>
                <strong>{row.value}</strong>
              </div>
            ))}
            <div className="auth-signal-foot"><span>LINUX</span><span>WINDOWS</span><span>MACOS</span></div>
          </div>
        </div>
      </motion.div>

      <div className="auth-brand-foot">
        <span><i aria-hidden="true" /> Encrypted response channel</span>
        <span>Role-governed access</span>
      </div>
    </aside>
  );
}
