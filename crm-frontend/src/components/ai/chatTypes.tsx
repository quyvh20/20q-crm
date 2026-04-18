import React from 'react';

export interface CommandEvent {
  type: 'thinking' | 'planning' | 'tool_result' | 'response' | 'confirm' | 'navigate' | 'form' | 'error' | 'done';
  message?: string;
  tool?: string;
  data?: unknown;
  done?: boolean;
}

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  events?: CommandEvent[];
  confirmPending?: ConfirmPayload;
  formPending?: FormPayload;
  navigatePending?: NavigatePayload;
  timestamp: Date;
}

export interface ConfirmPayload {
  tool: string;
  args: Record<string, unknown>;
  summary: string;
}

export interface FormPayload {
  form_type: 'contact' | 'deal';
  // contact prefills
  prefill_name?: string;
  prefill_email?: string;
  // deal prefills
  prefill_title?: string;
  prefill_value?: number;
}

export interface NavigatePayload {
  path: string;
  label: string;
}


