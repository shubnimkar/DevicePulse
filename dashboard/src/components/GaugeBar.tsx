import { motion } from 'motion/react';

interface GaugeBarProps {
	value: number;
	color?: string;
  height?: number;
}

export default function GaugeBar({ value, color, height = 4 }: GaugeBarProps) {
  const pct = Number.isFinite(value) ? Math.min(Math.max(value, 0), 100) : 0;

  return (
    <div
      className="gauge-track"
      style={{ height }}
      role="progressbar"
      aria-valuenow={pct}
      aria-valuemin={0}
      aria-valuemax={100}
    >
      <motion.div
        className="gauge-fill"
        initial={false}
        animate={{ width: `${pct}%` }}
        transition={{ type: 'spring', stiffness: 260, damping: 30 }}
        style={{
          background: color ?? 'var(--blue)',
        }}
      />
    </div>
  );
}
