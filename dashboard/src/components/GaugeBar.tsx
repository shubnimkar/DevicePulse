interface GaugeBarProps {
  value: number;
  color?: string;
  height?: number;
}

export default function GaugeBar({ value, color, height = 4 }: GaugeBarProps) {
  return (
    <div
      className="gauge-track"
      style={{ height }}
      role="progressbar"
      aria-valuenow={Math.min(value, 100)}
      aria-valuemin={0}
      aria-valuemax={100}
    >
      <div
        className="gauge-fill"
        style={{
          width: `${Math.min(value, 100)}%`,
          background: color ?? 'var(--blue)',
        }}
      />
    </div>
  );
}
