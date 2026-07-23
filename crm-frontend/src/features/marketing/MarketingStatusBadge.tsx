import React from 'react';
import { Badge } from '@/components/ui';
import { usePermissions } from '../../lib/auth';
import { useMarketingStatus } from './queries';

/** A compact marketing-standing badge for the contact detail header. Only renders
 *  for users with marketing.manage — the query is disabled for everyone else, so
 *  no /status request fires (it would only 403). Shows nothing when there is no
 *  signal yet (no suppression and no consent record), to avoid cluttering every
 *  contact with an "unknown" chip. */
export const MarketingStatusBadge: React.FC<{ email?: string | null }> = ({ email }) => {
  const { can } = usePermissions();
  const allowed = can('marketing.manage');
  // Hooks run unconditionally; the query self-disables when not allowed / no email.
  const { data, isSuccess } = useMarketingStatus(allowed ? email || undefined : undefined);
  if (!allowed || !email || !isSuccess || !data) return null;

  if (data.suppressed) {
    return (
      <Badge variant="destructive" title={`Do not email — ${data.suppression_reasons.join(', ') || 'suppressed'}`}>
        Do not email
      </Badge>
    );
  }

  switch (data.marketing_status) {
    case 'subscribed':
      return <Badge variant="success" title="Subscribed to marketing">Subscribed</Badge>;
    case 'unsubscribed':
      return <Badge variant="warning" title="Unsubscribed from marketing">Unsubscribed</Badge>;
    case 'pending':
      return <Badge variant="secondary" title="No marketing opt-in on record yet">Pending opt-in</Badge>;
    case 'cleaned':
      return <Badge variant="outline" title="Scrubbed after repeated bounces">Cleaned</Badge>;
    default:
      // No consent record and not suppressed — nothing meaningful to show.
      return null;
  }
};

export default MarketingStatusBadge;
