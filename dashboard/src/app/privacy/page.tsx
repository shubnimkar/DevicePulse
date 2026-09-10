import type { Metadata } from 'next';
import LegalPage, { PolicyCallout, PolicyTable } from '@/components/LegalPage';

export const metadata: Metadata = {
  title: 'Privacy Policy | DevicePulse',
  description: 'How DevicePulse deployments collect, use, store, retain and protect endpoint telemetry and account information.',
  alternates: { canonical: '/privacy' },
  robots: { index: true, follow: true },
  openGraph: {
    type: 'website',
    url: '/privacy',
    title: 'Privacy Policy | DevicePulse',
    description: 'DevicePulse endpoint telemetry privacy and data-handling practices.',
  },
};

const sections = [
  {
    id: 'scope-and-roles',
    title: 'Scope and responsibility',
    summary: 'DevicePulse is an endpoint operations platform deployed and administered by an organization for its managed devices and authorized users.',
    content: (
      <>
        <p>This policy describes the product-level data flows built into DevicePulse. The organization that deploys DevicePulse and determines why endpoint telemetry is collected is the <strong>Operator</strong>. The Operator is responsible for giving device users any notices required by employment, privacy, monitoring, or sector-specific law.</p>
        <p>This policy applies to the DevicePulse dashboard, API, and endpoint agent. It does not govern third-party infrastructure, identity providers, storage services, or websites that an Operator chooses to connect.</p>
        <PolicyCallout title="Operator notice controls">
          Your organization&apos;s workforce-monitoring or privacy notice may provide additional details, including its identity, legal basis, storage region, recipients, retention schedule, and rights-request channel.
        </PolicyCallout>
      </>
    ),
  },
  {
    id: 'data-collected',
    title: 'Information processed',
    summary: 'Collection is policy-controlled, but enabled collectors can process detailed technical and activity information from managed endpoints.',
    content: (
      <PolicyTable
        headers={['Category', 'Examples', 'Purpose']}
        rows={[
          ['Account data', 'Name, email address, role, account status, session metadata', 'Authenticate users and enforce dashboard permissions.'],
          ['Device identity', 'Device ID, hostname, operating system, architecture, hardware UUID, MAC address', 'Register, deduplicate, identify, and manage endpoints.'],
          ['Health and hardware', 'CPU, memory, disk, battery, network throughput, kernel and update status', 'Assess availability, capacity, and maintenance posture.'],
          ['Runtime and software', 'Processes, services, installed applications, open ports, process IDs, USB devices', 'Support inventory, diagnostics, and security review.'],
          ['Activity telemetry', 'Foreground application, focus duration, browser visit URL and timestamp', 'Provide authorized activity and usage reporting.'],
          ['Operational records', 'Policies, agent releases, build records, report configuration, audit-relevant timestamps', 'Operate and administer the deployment.'],
          ['Connection data', 'IP address and request metadata available to the API or hosting layer', 'Route traffic, enforce rate limits, troubleshoot, and protect the service.'],
        ]}
      />
    ),
  },
  {
    id: 'use-of-data',
    title: 'How information is used',
    content: (
      <>
        <p>DevicePulse processes information to register and authenticate endpoints, display fleet health, investigate device conditions, produce configured reports, apply collection and retention policy, deliver agent updates, and protect the deployment.</p>
        <p>The product does not include advertising, cross-context behavioral advertising, or data-broker functionality. An Operator must not repurpose collected telemetry in a way that conflicts with its notices, policies, contractual commitments, or applicable law.</p>
        <h3>Automated processing</h3>
        <p>DevicePulse can calculate health states, aggregate activity durations, and surface attention signals. These features support human review; the product does not independently make employment, credit, insurance, or similarly consequential decisions.</p>
      </>
    ),
  },
  {
    id: 'storage-retention',
    title: 'Storage and retention',
    content: (
      <>
        <p>Before upload, telemetry may be queued in a local SQLite database on the managed endpoint when the API is unavailable. Queued records are uploaded when connectivity returns.</p>
        <p>MongoDB stores device identity, current state, telemetry metadata, focus summaries, dashboard users, policies, and operational records. If configured, Amazon S3 or compatible object storage can hold raw telemetry and browser-history archives.</p>
        <ul>
          <li>Telemetry retention is configurable by the Operator; the product default is 30 days.</li>
          <li>MongoDB telemetry expiry uses a time-to-live index and may not occur at the exact expiry second.</li>
          <li>Object-storage retention is controlled separately through the Operator&apos;s bucket lifecycle configuration.</li>
          <li>Backups, logs, legal holds, and revocation records may follow separate documented schedules.</li>
        </ul>
      </>
    ),
  },
  {
    id: 'disclosure-transfers',
    title: 'Disclosure and transfers',
    content: (
      <>
        <p>Access is intended for authorized Operator personnel according to assigned roles. Data may also be processed by infrastructure providers selected by the Operator, such as hosting, database, object-storage, logging, or support providers.</p>
        <p>The Operator determines storage regions, subprocessors, and international-transfer safeguards. DevicePulse does not itself establish a universal hosting region because deployments can be self-hosted or use different infrastructure.</p>
      </>
    ),
  },
  {
    id: 'security',
    title: 'Security safeguards',
    content: (
      <>
        <p>DevicePulse supports role-based dashboard access, HTTP-only session cookies, hashed device API keys, revocable sessions, login rate limits, encrypted dashboard API responses, and configurable secure transport and storage.</p>
        <p>No system is completely secure. The Operator remains responsible for TLS termination, secret management, infrastructure access, backups, bucket policies, lifecycle controls, monitoring, and timely deployment of security updates. See the <a href="/security">Security &amp; Trust</a> page for implementation details and boundaries.</p>
      </>
    ),
  },
  {
    id: 'choices-rights',
    title: 'Choices and privacy rights',
    content: (
      <>
        <p>Depending on location and context, individuals may have rights to obtain information, access data, request correction or deletion, restrict or object to processing, or lodge a complaint with a regulator. These rights are not absolute and may vary for workplace data.</p>
        <p>Submit requests to the organization that provided or manages the DevicePulse deployment. DevicePulse administrators can investigate relevant account, device, telemetry, and archive records according to the Operator&apos;s procedures.</p>
      </>
    ),
  },
  {
    id: 'changes-contact',
    title: 'Changes and contact',
    content: (
      <>
        <p>This document should be reviewed when collectors, storage providers, purposes, retention settings, or deployment regions change. Material changes should be communicated through the Operator&apos;s established notice process.</p>
        <p>For privacy questions or requests, contact your DevicePulse administrator or the privacy contact named in your organization&apos;s deployment-specific notice.</p>
      </>
    ),
  },
];

export default function PrivacyPage() {
  return (
    <LegalPage
      slug="privacy"
      eyebrow="Data stewardship"
      title="Privacy Policy"
      description="A transparent account of the endpoint, activity, and account information a DevicePulse deployment can process—and the controls that shape its lifecycle."
      effectiveDate="10 September 2026"
      version="1.0"
      notice={<>DevicePulse deployments are organization-controlled. The Operator must identify itself and document any deployment-specific legal basis, recipients, storage region, retention rules, and contact details before production use.</>}
      sections={sections}
    />
  );
}
