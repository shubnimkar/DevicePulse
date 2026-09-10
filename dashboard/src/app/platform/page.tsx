import type { Metadata } from 'next';
import PlatformExperience from './platform-experience';

const siteUrl = process.env.NEXT_PUBLIC_SITE_URL ?? 'https://devicepulse.waybeyond.tech';

export const metadata: Metadata = {
  title: 'Endpoint Monitoring & Fleet Telemetry | DevicePulse',
  description: 'Monitor endpoint health, software inventory, processes, ports, browser activity and security posture across Linux, Windows and macOS.',
  alternates: { canonical: '/platform' },
  robots: {
    index: true,
    follow: true,
    googleBot: {
      index: true,
      follow: true,
      'max-image-preview': 'large',
      'max-snippet': -1,
      'max-video-preview': -1,
    },
  },
  openGraph: {
    type: 'website',
    url: '/platform',
    siteName: 'DevicePulse',
    title: 'Endpoint Monitoring & Fleet Telemetry | DevicePulse',
    description: 'A real-time endpoint monitoring platform for health, inventory, activity and security telemetry.',
  },
  twitter: {
    card: 'summary',
    title: 'Endpoint Monitoring & Fleet Telemetry | DevicePulse',
    description: 'Real-time endpoint health, inventory, activity and security telemetry in one operations console.',
  },
};

const softwareSchema = {
  '@context': 'https://schema.org',
  '@type': 'SoftwareApplication',
  name: 'DevicePulse',
  url: `${siteUrl}/platform`,
  applicationCategory: 'BusinessApplication',
  applicationSubCategory: 'Endpoint monitoring software',
  operatingSystem: ['Linux', 'Windows', 'macOS'],
  description: 'A real-time endpoint telemetry platform for monitoring device health, software inventory, processes, network ports, activity and security posture.',
  featureList: [
    'Real-time endpoint health monitoring',
    'Hardware and software inventory',
    'Process, service and network port telemetry',
    'Browser and application activity reporting',
    'Role-based fleet operations',
    'Offline telemetry queue',
  ],
};

const breadcrumbSchema = {
  '@context': 'https://schema.org',
  '@type': 'BreadcrumbList',
  itemListElement: [
    { '@type': 'ListItem', position: 1, name: 'DevicePulse', item: siteUrl },
    { '@type': 'ListItem', position: 2, name: 'Platform', item: `${siteUrl}/platform` },
  ],
};

export default function PlatformPage() {
  return (
    <>
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(softwareSchema) }} />
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(breadcrumbSchema) }} />
      <PlatformExperience />
    </>
  );
}
