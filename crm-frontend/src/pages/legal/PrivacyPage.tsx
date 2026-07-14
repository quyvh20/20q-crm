import LegalPageShell from './LegalPageShell';
import { useDocumentTitle } from '../../lib/useDocumentTitle';

// The in-app Privacy fallback (U7.6). See LegalPageShell for why this deliberately
// contains no invented legal text.
export default function PrivacyPage() {
  useDocumentTitle('Privacy Policy');

  return (
    <LegalPageShell
      heading="Privacy Policy"
      outline={[
        'The data controller: who decides how personal data in this workspace is used, and how to reach them.',
        'What is collected — account details, the CRM records members enter, authentication metadata, and usage/telemetry.',
        'Why each category is collected, and the lawful basis for it where that applies (e.g. GDPR).',
        'Sub-processors and third parties the deployment actually uses — email delivery, error tracking, AI model providers, hosting.',
        'Whether any content is sent to third-party AI providers, and whether it is used for training.',
        'Where data is stored and processed, and how it moves across borders.',
        'How long each category is retained, and what deletion actually removes.',
        'The rights a person has over their data — access, correction, export, erasure — and how to exercise them.',
        'How a security incident is handled and disclosed.',
        'How changes to this policy are notified.',
      ]}
    >
      <p>
        This page does not describe how any particular deployment of this software handles personal
        data — only the operator of the workspace can say that, because they choose the hosting, the
        integrations, and the AI providers involved. Ask them for their actual policy before entering
        anyone else’s personal data into a workspace.
      </p>
    </LegalPageShell>
  );
}
