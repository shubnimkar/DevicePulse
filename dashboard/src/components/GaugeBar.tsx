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
      <div
        className="gauge-fill"
        style={{
          width: `${pct}%`,
          background: color ?? 'var(--blue)',
        }}
      />
    </div>
  );
}
