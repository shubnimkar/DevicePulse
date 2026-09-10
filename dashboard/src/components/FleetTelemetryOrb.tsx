'use client';

import { motion, useReducedMotion } from 'motion/react';

interface FleetTelemetryOrbProps {
  connectedPercent: number;
  online: number;
  total: number;
}

export default function FleetTelemetryOrb({ connectedPercent, online, total }: FleetTelemetryOrbProps) {
  const reduceMotion = useReducedMotion();
  const percent = Number.isFinite(connectedPercent)
    ? Math.min(100, Math.max(0, connectedPercent))
    : 0;
  const circumference = 2 * Math.PI * 52;
  const dashOffset = circumference * (1 - percent / 100);

  return (
    <div className="telemetry-orb" role="img" aria-label={`${online} of ${total} endpoints connected, ${percent.toFixed(0)} percent`}>
      <div className="telemetry-orb-visual" aria-hidden="true">
        <motion.div
          className="orb-sweep"
          animate={reduceMotion ? undefined : { rotate: 360 }}
          transition={{ duration: 7, ease: 'linear', repeat: Infinity }}
        />
        <svg viewBox="0 0 120 120">
          <circle className="orb-track" cx="60" cy="60" r="52" />
          <motion.circle
            className="orb-value"
            cx="60"
            cy="60"
            r="52"
            initial={reduceMotion ? false : { strokeDashoffset: circumference }}
            animate={{ strokeDashoffset: dashOffset }}
            transition={{ duration: reduceMotion ? 0 : 0.8, ease: [0.22, 1, 0.36, 1] }}
            style={{ strokeDasharray: circumference }}
          />
        </svg>
        <span className="orb-core"><strong>{percent.toFixed(0)}%</strong><small>connected</small></span>
        <i className="orb-node node-a" /><i className="orb-node node-b" /><i className="orb-node node-c" />
      </div>
      <div className="telemetry-orb-copy">
        <span>Fleet signal</span>
        <strong>{online} / {total || 0} reporting</strong>
        <small>Calculated from the latest endpoint check-ins</small>
      </div>
    </div>
  );
}
