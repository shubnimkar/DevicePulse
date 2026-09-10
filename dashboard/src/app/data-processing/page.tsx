import type { Metadata } from 'next';
import LegalPage, { PolicyCallout, PolicyTable } from '@/components/LegalPage';

export const metadata: Metadata = {
  title: 'Data Processing | DevicePulse',
  description: 'DevicePulse data-processing scope, data categories, retention, deletion, security and deployment responsibilities.',
  alternates: { canonical: '/data-processing' },
  robots: { index: true, follow: true },
  openGraph: { type: 'website', url: '/data-processing', title: 'Data Processing | DevicePulse', description: 'Data-processing details for DevicePulse endpoint telemetry deployments.' },
};

const sections = [
  {
    id: 'processing-parties',
    title: 'Parties and roles',
    content: (
      <>
        <p>The organization that determines why and how DevicePulse is used is the <strong>Operator</strong>. In privacy-law terminology, the Operator will commonly act as controller or business. A service provider hosting or supporting DevicePulse may act as processor or service provider under a separate written agreement.</p>
        <p>Role allocation depends on the actual deployment, contracts, and jurisdiction. This page describes the technical processing schedule and is not, by itself, a signed data processing agreement.</p>
      </>
    ),
  },
  {
    id: 'subject-matter',
    title: 'Subject matter and duration',
    content: (
      <PolicyTable
        headers={['Element', 'Processing detail']}
        rows={[
          ['Subject matter', 'Collection, transmission, storage, organization, display, aggregation, retrieval, reporting, deletion, and protection of endpoint operations data.'],
          ['Purpose', 'Endpoint inventory, health monitoring, diagnostics, operational reporting, security review, policy administration, and authorized activity analysis.'],
          ['Duration', 'For the period the deployment is active, plus configured retention, backup, incident, legal-hold, and deletion periods.'],
          ['Frequency', 'Collector-dependent. Many signals can be collected each sync; installed applications and OS updates are typically cached and refreshed less frequently.'],
          ['Geography', 'Determined by the Operator&apos;s endpoint locations and selected hosting, database, object-storage, backup, and support regions.'],
        ]}
      />
    ),
  },
  {
    id: 'data-subjects-categories',
    title: 'People and data categories',
    content: (
      <>
        <h3>Categories of people</h3>
        <p>Authorized dashboard users; employees, contractors, or other users of managed endpoints; administrators; and individuals whose identifiers may appear in device names, account records, application data, or browsed URLs.</p>
        <h3>Categories of data</h3>
        <ul>
          <li>Identity and contact details: name, work email, role, account status.</li>
          <li>Device and online identifiers: device ID, hostname, hardware UUID, MAC address, IP address.</li>
          <li>Technical data: OS, hardware utilization, applications, processes, services, ports, updates, USB devices.</li>
          <li>Activity data: active application, focus duration, browser URL and visit timestamp.</li>
          <li>Security and operational data: sessions, policy settings, release records, report configuration, event timestamps.</li>
        </ul>
        <PolicyCallout title="Sensitive data can appear indirectly">
          DevicePulse is not designed to collect special-category or highly sensitive content, but application names, URLs, device names, and operational metadata can reveal sensitive inferences. Operators should disable unnecessary collectors and restrict access accordingly.
        </PolicyCallout>
      </>
    ),
  },
  {
    id: 'instructions-controls',
    title: 'Instructions and controls',
    content: (
      <p>Documented instructions are expressed through deployment configuration, collection policy, assigned roles, retention settings, administrative actions, and any governing agreement. The Operator is responsible for ensuring instructions are lawful. A processor should notify the Operator if an instruction appears unlawful, where permitted, and should not use data for an independent purpose.</p>
    ),
  },
  {
    id: 'confidentiality-security',
    title: 'Confidentiality and security',
    content: (
      <ul>
        <li>Limit access to authorized personnel subject to appropriate confidentiality duties.</li>
        <li>Use role-based accounts, protected sessions, per-device credentials, rate limits, and credential revocation.</li>
        <li>Protect network traffic with TLS and configure available application-level response encryption.</li>
        <li>Configure encryption, access policy, lifecycle, logging, and recovery for databases and object storage.</li>
        <li>Maintain vulnerability, patch, incident-response, continuity, and access-review procedures.</li>
        <li>Assess safeguards regularly in light of the data, threats, deployment model, and current product version.</li>
      </ul>
    ),
  },
  {
    id: 'subprocessors-transfers',
    title: 'Subprocessors and transfers',
    content: (
      <p>DevicePulse can be self-hosted and does not impose a fixed subprocessor list. The Operator must document every provider that can process deployment data—including hosting, MongoDB, object storage, logging, backup, support, email, and identity services—and establish any required contractual and international-transfer safeguards before use.</p>
    ),
  },
  {
    id: 'retention-deletion',
    title: 'Retention, return, and deletion',
    content: (
      <>
        <p>The standard telemetry setting defaults to 30 days and can be changed through policy. MongoDB expiry applies to telemetry records carrying an expiry timestamp. Object-storage archives require separate lifecycle rules and should be configured to match the approved schedule.</p>
        <p>Device deletion removes active device data according to application behavior and creates a revocation record for hardware identifiers. Operators must separately address backups, logs, archives, reports, legal holds, and copies exported outside DevicePulse.</p>
      </>
    ),
  },
  {
    id: 'assistance-audit',
    title: 'Assistance and assurance',
    content: (
      <p>Subject to the governing agreement and available information, processors should assist the Operator with rights requests, security assessments, incident investigation, deletion, and regulatory inquiries. The Operator should retain configuration, access-review, retention, subprocessor, testing, and incident evidence appropriate to its obligations.</p>
    ),
  },
  {
    id: 'completion',
    title: 'Required completion before use',
    content: (
      <ol>
        <li>Name the controller/business and every processor or service provider.</li>
        <li>Record purposes, lawful basis, affected workforce groups, and enabled collectors.</li>
        <li>Confirm hosting regions, subprocessors, transfer safeguards, and support access.</li>
        <li>Set retention for MongoDB, object storage, backups, logs, exports, and revocation records.</li>
        <li>Add rights-request, incident, legal, security, and privacy contacts.</li>
        <li>Execute any required data processing agreement and complete legal or workforce consultation.</li>
      </ol>
    ),
  },
];

export default function DataProcessingPage() {
  return (
    <LegalPage
      slug="data-processing"
      eyebrow="Processing schedule"
      title="Data Processing"
      description="The operational schedule behind DevicePulse telemetry: parties, purposes, data categories, safeguards, retention, deletion, and deployment decisions."
      effectiveDate="10 September 2026"
      version="1.0"
      notice={<>This page is a technical processing schedule, not an executed data processing agreement. Complete the listed organization, subprocessor, transfer, contact, and jurisdiction fields in the governing contract for each deployment.</>}
      sections={sections}
    />
  );
}
