import LegalPageShell from './LegalPageShell';
import { useDocumentTitle } from '../../lib/useDocumentTitle';

// The in-app Terms fallback (U7.6). See LegalPageShell for why this deliberately
// contains no invented legal text.
export default function TermsPage() {
  useDocumentTitle('Terms of Service');

  return (
    <LegalPageShell
      heading="Terms of Service"
      outline={[
        'Who the agreement is between — the operating entity, its jurisdiction, and how to contact it.',
        'What the service is, and what level of availability (if any) is promised.',
        'Acceptable use: what a customer may not do with the workspace, and what happens when they do.',
        'Accounts, workspaces, and who is responsible for the data a member imports or creates.',
        'Fees, billing cycles, renewals, refunds, and taxes — if the workspace is a paid one.',
        'Ownership: the operator’s rights in the software, and the customer’s rights in their own data.',
        'Suspension and termination — by either side — and what happens to the data afterwards.',
        'Warranties, disclaimers, limitation of liability, and indemnities.',
        'Governing law, dispute resolution, and how changes to these terms are notified.',
      ]}
    >
      <p>
        Until the operator of this workspace publishes their own terms, nothing on this page creates
        or restricts any right. If you were sent here from a sign-up screen and need to know the
        actual terms before creating an account, ask the person or company who invited you.
      </p>
    </LegalPageShell>
  );
}
