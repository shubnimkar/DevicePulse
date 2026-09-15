'use client';

export default function AuthMissionPanel() {
  return (
    <div className="auth-floating-backdrop" aria-hidden="true">
      <div className="auth-floating-grid" />
      <div className="auth-floating-halo" />

      <svg
        className="auth-floating-wave"
        viewBox="0 0 1440 520"
        preserveAspectRatio="none"
        focusable="false"
      >
        <defs>
          <linearGradient id="floating-wave-gradient" x1="0" y1="0" x2="1" y2="0">
            <stop offset="0" stopColor="#45e7d0" stopOpacity="0" />
            <stop offset="0.18" stopColor="#45e7d0" stopOpacity="0.52" />
            <stop offset="0.5" stopColor="#8af7e6" stopOpacity="0.88" />
            <stop offset="0.82" stopColor="#65b9ff" stopOpacity="0.48" />
            <stop offset="1" stopColor="#65b9ff" stopOpacity="0" />
          </linearGradient>
          <filter id="floating-wave-glow" x="-20%" y="-100%" width="140%" height="300%">
            <feGaussianBlur stdDeviation="11" result="blur" />
            <feMerge><feMergeNode in="blur" /><feMergeNode in="SourceGraphic" /></feMerge>
          </filter>
        </defs>
        <path
          className="auth-floating-wave-soft"
          d="M0 284H276l26-8 20-62 21 132 26-201 27 252 27-175 25 92 27-30h112l21-9 18-48 20 104 25-167 27 206 27-139 24 75 29-22h116l28-8 18-45 22 98 26-151 28 183 26-122 24 64 28-18h286"
        />
        <path
          d="M0 284H276l26-8 20-62 21 132 26-201 27 252 27-175 25 92 27-30h112l21-9 18-48 20 104 25-167 27 206 27-139 24 75 29-22h116l28-8 18-45 22 98 26-151 28 183 26-122 24 64 28-18h286"
        />
      </svg>

      <div className="auth-floating-rings">
        <span className="ring ring-one" />
        <span className="ring ring-two" />
        <span className="ring ring-three" />
        <span className="ring ring-four" />
      </div>

      <div className="auth-floating-landscape landscape-left" />
      <div className="auth-floating-landscape landscape-right" />
      <span className="auth-floating-node node-north" />
      <span className="auth-floating-node node-west" />
      <span className="auth-floating-node node-east" />
      <span className="auth-floating-node node-south" />

      <div className="auth-floating-status status-left">
        <span className="status-dot" />
        Protected channel
      </div>
      <div className="auth-floating-status status-right">Endpoint operations · 24/7</div>
    </div>
  );
}
