import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "DevicePulse — Endpoint Telemetry",
  description: "Real-time enterprise endpoint monitoring and telemetry dashboard",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
