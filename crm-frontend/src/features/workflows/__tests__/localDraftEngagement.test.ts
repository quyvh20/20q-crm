import { describe, it, expect } from 'vitest';
import { localDraftFromPrompt } from '../builder/localDraft';

// The offline copilot fallback runs when the AI service is unreachable and its
// draft lands straight on the canvas behind a review banner. Two engagement
// triggers now share the word "email", so these pin which one wins and, just as
// importantly, that neither grows a send_email step the user never asked for.

const trigger = (p: string) => localDraftFromPrompt(p).trigger.type;
const stepTypes = (p: string) =>
  localDraftFromPrompt(p).steps.map((s) => (s.type === 'action' ? s.action!.type : s.type));

describe('localDraft — engagement triggers', () => {
  it('routes click prompts to email_clicked', () => {
    expect(trigger('when someone clicks a link in a campaign email, create a follow-up task')).toBe('email_clicked');
    expect(trigger('if a contact clicks the CTA, notify the owner')).toBe('email_clicked');
    expect(trigger('when the pricing link is clicked, create a task')).toBe('email_clicked');
  });

  it('leaves open prompts on email_opened', () => {
    expect(trigger('when someone opens our newsletter email, create a task')).toBe('email_opened');
    expect(trigger('when a contact opens an email, notify the owner')).toBe('email_opened');
  });

  it('does not hijack a plain send-email prompt', () => {
    expect(trigger('when a contact is created, send them a welcome email')).toBe('contact_created');
    expect(stepTypes('when a contact is created, send them a welcome email')).toContain('send_email');
  });

  it('does not add a send_email step to either engagement draft', () => {
    // "email" here names the trigger, not an instruction to send one.
    expect(stepTypes('when someone clicks a link in a campaign email, create a follow-up task'))
      .not.toContain('send_email');
    expect(stepTypes('when someone opens a campaign email, create a follow-up task'))
      .not.toContain('send_email');
  });

  it('still honours an explicit send instruction on an engagement trigger', () => {
    expect(stepTypes('when someone clicks a link in an email, send them a follow-up email'))
      .toContain('send_email');
  });
});
