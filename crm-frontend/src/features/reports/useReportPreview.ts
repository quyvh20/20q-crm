import { useEffect, useState } from 'react';
import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { previewReport, type ReportConfig, type ReportResult } from '../../lib/api';

// A config is runnable once it has enough to produce SOMETHING: a group field
// for grouped charts, at least one column for tables; a KPI just counts.
export function isRunnableConfig(cfg: ReportConfig): boolean {
  if (cfg.chart === 'table') return (cfg.columns?.length ?? 0) > 0;
  if (cfg.chart === 'kpi') return true;
  return Boolean(cfg.group_by?.field);
}

// Live preview for the builder: debounce config edits ~450ms, then run the
// unsaved config server-side (where OLS/FLS/data scope apply). Previous data
// is kept while the next preview loads so the chart doesn't flicker.
export function useReportPreview(slug: string | undefined, config: ReportConfig) {
  const [debounced, setDebounced] = useState(config);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(config), 450);
    return () => clearTimeout(t);
  }, [config]);

  return useQuery<ReportResult, Error>({
    queryKey: ['report-preview', slug, debounced],
    queryFn: () => previewReport(slug!, debounced),
    enabled: Boolean(slug) && isRunnableConfig(debounced),
    placeholderData: keepPreviousData,
    staleTime: 10_000,
    retry: false,
  });
}
