import type { Metadata } from 'next';
import LegalPage, { PolicyCallout } from '@/components/LegalPage';

export const metadata: Metadata = {
  title: 'Terms of Use | DevicePulse',
  description: 'Terms governing authorized access to and use of DevicePulse endpoint operations deployments.',
  alternates: { canonical: '/terms' },
  robots: { index: true, follow: true },
  openGraph: { type: 'website', url: '/terms', title: 'Terms of Use | DevicePulse', description: 'Authorized-use terms for DevicePulse deployments.' },
};

const sections = [
  {
    id: 'acceptance',
    title: 'Acceptance and authority',
    content: (
      <>
        <p>These terms govern access to a DevicePulse deployment. By using the dashboard, API, or endpoint agent, you confirm that you are authorized to act for the organization operating the deployment and agree to follow its policies.</p>
        <p>If a separate written agreement applies, that agreement controls where it conflicts with these product-level terms.</p>
      </>
    ),
  },
  {
    id: 'authorized-use',
    title: 'Authorized use',
    summary: 'DevicePulse is intended for legitimate administration, observability, security, inventory, and reporting on endpoints the Operator is authorized to manage.',
    content: (
      <>
        <p>The Operator must obtain all permissions and provide all notices required before deploying the agent or enabling activity collectors. Access must be limited to personnel with a genuine operational need.</p>
        <PolicyCallout title="Monitoring responsibility">
          DevicePulse can process browser and application activity. Enabling these collectors without appropriate authority or notice may violate workplace, privacy, communications, or computer-misuse laws.
        </PolicyCallout>
      </>
    ),
  },
  {
    id: 'accounts',
    title: 'Accounts and credentials',
    content: (
      <ul>
        <li>Keep dashboard credentials, session access, enrollment material, and device API keys confidential.</li>
        <li>Use individual accounts and the least-privileged role appropriate to each user.</li>
        <li>Promptly deactivate accounts and revoke endpoints that no longer require access.</li>
        <li>Notify the Operator if credentials may be compromised or if unauthorized activity is suspected.</li>
        <li>You are responsible for activity performed through accounts and devices under your control.</li>
      </ul>
    ),
  },
  {
    id: 'prohibited-use',
    title: 'Prohibited conduct',
    content: (
      <>
        <p>You must not use DevicePulse to access or monitor systems without authorization, violate individual rights, introduce malicious code, evade security controls, disrupt service, probe third-party infrastructure, falsify telemetry, or attempt to obtain another user&apos;s credentials.</p>
        <p>You must not expose dashboard or API endpoints without appropriate authentication, transport security, network controls, and secret management.</p>
      </>
    ),
  },
  {
    id: 'operator-duties',
    title: 'Operator responsibilities',
    content: (
      <ul>
        <li>Configure collectors, roles, retention, storage, and network access for the deployment&apos;s actual purpose.</li>
        <li>Maintain accurate privacy, monitoring, retention, incident-response, and acceptable-use notices.</li>
        <li>Secure hosting infrastructure, databases, object storage, TLS, backups, and administrative secrets.</li>
        <li>Test upgrades and maintain supported versions of DevicePulse and its dependencies.</li>
        <li>Respond to user requests, incidents, regulatory duties, and legal holds applicable to the Operator.</li>
      </ul>
    ),
  },
  {
    id: 'availability-changes',
    title: 'Availability and changes',
    content: (
      <p>DevicePulse may evolve, and features may change, be suspended, or be removed. Availability depends on endpoint permissions, network connectivity, third-party infrastructure, deployment configuration, and supported operating systems. The Operator should maintain recovery procedures and must not rely on DevicePulse as the sole source for emergency response or legally required records.</p>
    ),
  },
  {
    id: 'ownership-feedback',
    title: 'Ownership and feedback',
    content: (
      <p>DevicePulse software, branding, documentation, and associated intellectual property remain subject to their applicable ownership and license terms. The Operator retains responsibility for its data. Feedback may be used to improve DevicePulse without an obligation to adopt it, subject to any controlling written agreement.</p>
    ),
  },
  {
    id: 'disclaimers-liability',
    title: 'Disclaimers and liability',
    content: (
      <>
        <p>Unless a controlling written agreement says otherwise, DevicePulse is provided on an “as available” basis without warranties that it will be uninterrupted, error-free, or suitable for every legal or operational requirement.</p>
        <p>Any warranty exclusions, liability limits, indemnities, governing law, and dispute terms must be completed by the legal entity offering the deployment and may be limited by applicable law.</p>
      </>
    ),
  },
  {
    id: 'termination-contact',
    title: 'Suspension, termination, and contact',
    content: (
      <p>Access may be suspended or terminated to protect the deployment, comply with law, respond to misuse, or enforce an applicable agreement. Questions should be directed to your DevicePulse administrator or the contact named by the organization operating the deployment.</p>
    ),
  },
];

export default function TermsPage() {
  return (
    <LegalPage
      slug="terms"
      eyebrow="Responsible operation"
      title="Terms of Use"
      description="The operating rules for authorized access to DevicePulse, its endpoint agent, dashboard, API, and collected telemetry."
      effectiveDate="10 September 2026"
      version="1.0"
      notice={<>These are product-level template terms, not a substitute for a signed customer agreement. The entity providing your deployment should add its legal name, address, governing law, commercial terms, and contact channel.</>}
      sections={sections}
    />
  );
}
