import React from 'react';
import type { ConfirmPayload } from './chatTypes';

interface Props {
  payload: ConfirmPayload;
  onConfirm: (payload: ConfirmPayload) => void;
  onCancel: () => void;
}

const toolLabels: Record<string, string> = {
  update_deal: '✏️ Update Deal',
  create_task: '✅ Create Task',
  log_activity: '📝 Log Activity',
  create_contact: '👤 Create Contact',
};

export default function ConfirmBanner({ payload, onConfirm, onCancel }: Props) {
  return (
    <div style={styles.wrapper}>
      <div style={styles.header}>
        <span style={styles.icon}>⚠️</span>
        <span style={styles.label}>{toolLabels[payload.tool] || payload.tool}</span>
      </div>
      <p style={styles.summary}>{payload.summary}</p>
      <div style={styles.actions}>
        <button style={styles.cancelBtn} onClick={onCancel}>Cancel</button>
        <button style={styles.confirmBtn} onClick={() => onConfirm(payload)}>
          Confirm
        </button>
      </div>
    </div>
  );
}

const styles: Record<string, React.CSSProperties> = {
  wrapper: {
    border: '1px solid #f59e0b',
    borderRadius: 12,
    background: 'linear-gradient(135deg, rgba(245,158,11,0.06), rgba(239,68,68,0.04))',
    padding: '10px 14px',
    marginBottom: 6,
    animation: 'fadeSlide 0.2s ease',
  },
  header: {
    display: 'flex',
    alignItems: 'center',
    gap: 6,
    marginBottom: 6,
  },
  icon: { fontSize: 15 },
  label: { fontWeight: 700, fontSize: 13, color: '#b45309' },
  summary: { fontSize: 12, color: 'var(--muted-foreground)', margin: '0 0 10px' },
  actions: { display: 'flex', gap: 8, justifyContent: 'flex-end' },
  cancelBtn: {
    padding: '5px 14px',
    borderRadius: 8,
    border: '1px solid var(--border)',
    background: 'transparent',
    cursor: 'pointer',
    fontSize: 12,
    color: 'var(--muted-foreground)',
  },
  confirmBtn: {
    padding: '5px 14px',
    borderRadius: 8,
    border: 'none',
    background: 'linear-gradient(135deg, #f59e0b, #ef4444)',
    color: '#fff',
    cursor: 'pointer',
    fontSize: 12,
    fontWeight: 700,
  },
};
