import { Badge } from '../../components/ui/badge';

// LeadScoreBadge renders a contact's 0-100 lead score (R9.4).
//
// Bands are hardcoded at 70/30 to match probabilityVariant on the deal page,
// so a "hot" score and a "likely" deal read the same way across the product.
// Making them configurable is deliberately deferred: a per-org threshold is a
// settings surface, a migration and a second thing to explain, for a colour.
function scoreVariant(score: number): 'success' | 'warning' | 'secondary' {
  if (score >= 70) return 'success';
  if (score >= 30) return 'warning';
  return 'secondary';
}

export default function LeadScoreBadge({
  score,
  className,
}: {
  score: number | undefined;
  className?: string;
}) {
  // Undefined means the field was not projected (an older API response), which
  // is different from a real 0 — show nothing rather than a misleading "cold".
  if (score === undefined || score === null) return null;
  return (
    <Badge variant={scoreVariant(score)} className={className} title="Lead score (0–100)">
      {score}
    </Badge>
  );
}
