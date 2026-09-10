import type { Metadata } from 'next';
import LegalPage, { PolicyCallout, PolicyTable } from '@/components/LegalPage';

export const metadata: Metadata = {
  title: 'Security & Trust | DevicePulse',
  description: 'DevicePulse security architecture, authentication, encryption, access control and deployment responsibilities.',
  alternates: { canonical: '/security' },
  robots: { index: true, follow: true },
  openGraph: { type: 'website', url: '/security', title: 'Security & Trust | DevicePulse', description: 'Security architecture and operational boundaries for DevicePulse.' },
};

const sections = [
  {
    id: 'security-model',
    title: 'Security model',
    summary: 'DevicePulse separates endpoint identity, dashboard identity, collection policy, and storage so each layer can be controlled independently.',
    content: (
      <PolicyTable
        headers={['Layer', 'Built-in control', 'Operator responsibility']}
        rows={[
          ['Endpoint agent', 'Per-device API credentials, hardware-aware registration, credential rotation, local queue', 'Secure installation, host permissions, enrollment path, and endpoint lifecycle.'],
          ['API', 'Authenticated ingest, request limits, bounded payloads, role checks, response-encryption protocol', 'Network exposure, TLS, runtime secrets, patching, and monitoring.'],
          ['Dashboard', 'HTTP-only sessions, role-based access, session revocation after password reset', 'User provisioning, role review, password policy, and trusted administrator access.'],
          ['Storage', 'TTL-ready telemetry records and optional separation of metadata from raw archives', 'Database access, encryption settings, bucket policy, lifecycle rules, backups, and regions.'],
        ]}
      />
    ),
  },
  {
    id: 'identity-access',
    title: 'Identity and access control',
    content: (
      <>
        <p>Dashboard users authenticate through a signed, HTTP-only session cookie. Accounts are assigned admin, manager, or viewer roles, and the API rechecks account status and role for protected requests. Passwords are stored as bcrypt hashes.</p>
        <p>Device agents use separate API credentials. The server stores a hash of each device key, supports key rotation, and can retain hardware-identifier revocation records to stop deleted endpoints from silently re-registering.</p>
        <ul>
          <li><strong>Admin:</strong> full access, including user administration and device deletion.</li>
          <li><strong>Manager:</strong> telemetry access and policy or settings management.</li>
          <li><strong>Viewer:</strong> read-only access to telemetry and settings.</li>
        </ul>
      </>
    ),
  },
  {
    id: 'encryption',
    title: 'Encryption and transport',
    content: (
      <>
        <p>The dashboard client supports per-request encrypted API response bodies using ephemeral ECDH key agreement and AES-GCM. Secure session cookies are required for non-local dashboard origins.</p>
        <p>TLS termination and encryption at rest depend on the Operator&apos;s deployment. MongoDB, object storage, disks, backups, and logs must be configured and verified separately.</p>
        <PolicyCallout title="No blanket certification claim">
          This page describes product capabilities, not a SOC 2, ISO 27001, HIPAA, PCI DSS, or government authorization. Configuration and operational evidence determine whether a deployment meets a particular framework.
        </PolicyCallout>
      </>
    ),
  },
  {
    id: 'application-protection',
    title: 'Application protections',
    content: (
      <ul>
        <li>Login and device-registration rate limits reduce automated abuse.</li>
        <li>Ingested device identity is bound server-side to the authenticated device key.</li>
        <li>Request body limits and bounded history reads reduce resource-exhaustion risk.</li>
        <li>Password resets invalidate older sessions through a per-user token epoch.</li>
        <li>Secure, HTTP-only cookies reduce client-side credential exposure.</li>
        <li>Role checks protect privileged dashboard and API operations.</li>
      </ul>
    ),
  },
  {
    id: 'deployment-hardening',
    title: 'Deployment hardening',
    content: (
      <ol>
        <li>Place the API and dashboard behind maintained TLS termination and restrict direct backend access.</li>
        <li>Use high-entropy session and administrative secrets stored outside source control.</li>
        <li>Limit database and object-storage permissions to the minimum required actions and paths.</li>
        <li>Enable storage encryption, versioning or recovery controls, lifecycle rules, and access logging appropriate to the data.</li>
        <li>Restrict administrators, review roles regularly, remove stale accounts, and rotate credentials after exposure.</li>
        <li>Monitor authentication, ingest, storage, build, and deployment events; test restoration and incident procedures.</li>
        <li>Keep the Go agent, API, Next.js dashboard, operating systems, and dependencies supported and patched.</li>
      </ol>
    ),
  },
  {
    id: 'vulnerability-management',
    title: 'Vulnerability management',
    content: (
      <p>Security findings should include the affected version, deployment context, reproduction steps, impact, and a safe contact method. Do not test against production systems, access data that is not yours, degrade availability, or publicly disclose an unresolved issue without authorization. Report findings to the security contact designated by the organization operating your DevicePulse deployment.</p>
    ),
  },
  {
    id: 'incident-response',
    title: 'Incident response',
    content: (
      <p>The Operator should maintain procedures to identify affected endpoints and users, contain access, rotate device and session credentials, preserve appropriate evidence, assess notifications, recover service, and document corrective actions. Product telemetry can support investigation but does not replace centralized security logging or an incident-response program.</p>
    ),
  },
  {
    id: 'assurance-contact',
    title: 'Assurance and contact',
    content: (
      <p>Security posture changes with architecture, configuration, providers, and release version. Operators should conduct risk assessment, code and dependency review, penetration testing, configuration validation, and access review appropriate to their environment. Contact your DevicePulse administrator for the deployment&apos;s current evidence and security channel.</p>
    ),
  },
];

export default function SecurityPage() {
  return (
    <LegalPage
      slug="security"
      eyebrow="Security architecture"
      title="Security & Trust"
      description="A precise view of DevicePulse security controls, configuration boundaries, and the responsibilities required to operate endpoint telemetry safely."
      effectiveDate="10 September 2026"
      version="1.0"
      notice={<>Security is shared between DevicePulse and the organization operating it. This page describes supported controls; it does not attest that every deployment enables, configures, or continuously verifies them.</>}
      sections={sections}
    />
  );
}
