'use client';

import { motion, useMotionValue, useReducedMotion, useSpring, useTransform } from 'motion/react';
import type { CSSProperties, PointerEvent, ReactNode } from 'react';

type MetricTone = 'neutral' | 'online' | 'offline' | 'attention';

interface MissionMetricTileProps {
  label: string;
  value: ReactNode;
  detail: string;
  tone?: MetricTone;
  onClick: () => void;
}

export default function MissionMetricTile({
  label,
  value,
  detail,
  tone = 'neutral',
  onClick,
}: MissionMetricTileProps) {
  const reduceMotion = useReducedMotion();
  const pointerX = useMotionValue(0);
  const pointerY = useMotionValue(0);
  const smoothX = useSpring(pointerX, { stiffness: 240, damping: 28, mass: 0.55 });
  const smoothY = useSpring(pointerY, { stiffness: 240, damping: 28, mass: 0.55 });
  const rotateX = useTransform(smoothY, [-0.5, 0.5], [3.5, -3.5]);
  const rotateY = useTransform(smoothX, [-0.5, 0.5], [-4, 4]);
  const glowX = useTransform(smoothX, [-0.5, 0.5], ['18%', '82%']);
  const glowY = useTransform(smoothY, [-0.5, 0.5], ['18%', '82%']);

  const updatePointer = (event: PointerEvent<HTMLButtonElement>) => {
    if (reduceMotion || event.pointerType === 'touch') return;
    const bounds = event.currentTarget.getBoundingClientRect();
    pointerX.set((event.clientX - bounds.left) / bounds.width - 0.5);
    pointerY.set((event.clientY - bounds.top) / bounds.height - 0.5);
  };

  const resetPointer = () => {
    pointerX.set(0);
    pointerY.set(0);
  };

  return (
    <motion.button
      type="button"
      className={`dashboard-tile mission-metric mission-metric-${tone}`}
      onClick={onClick}
      onPointerMove={updatePointer}
      onPointerLeave={resetPointer}
      onBlur={resetPointer}
      whileHover={reduceMotion ? undefined : { y: -3, scale: 1.008 }}
      whileTap={reduceMotion ? undefined : { scale: 0.985 }}
      style={{
        rotateX: reduceMotion ? 0 : rotateX,
        rotateY: reduceMotion ? 0 : rotateY,
        transformPerspective: 850,
        transformStyle: 'preserve-3d',
        '--glow-x': glowX,
        '--glow-y': glowY,
      } as CSSProperties}
    >
      <span className="metric-grid" aria-hidden="true" />
      <span className="metric-signal" aria-hidden="true"><i /><i /><i /></span>
      <span className="metric-label">{label}</span>
      <strong>{value}</strong>
      <small>{detail}</small>
      <span className="metric-action" aria-hidden="true">Inspect <b>↗</b></span>
    </motion.button>
  );
}
