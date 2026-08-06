interface GaugeBarProps {
  value: number;
  color?: string;
  height?: number;
  animated?: boolean;
}

export default function GaugeBar({ value, color, height = 5, animated = true }: GaugeBarProps) {
  return (
    <div
      className="gauge-wrap"
      style={{ height }}
      role="progressbar"
      aria-valuenow={Math.min(value, 100)}
      aria-valuemin={0}
      aria-valuemax={100}
    >
      <div
        className={`gauge-bar${animated ? ' gauge-animated' : ''}`}
        style={{
          width: `${Math.min(value, 100)}%`,
          background: color ?? 'var(--accent-gradient)',
        }}
      />
    </div>
  );
}
