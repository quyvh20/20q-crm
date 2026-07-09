import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { EmailTemplateBodyEditor } from '../EmailTemplateBodyEditor';

// Mounts the real TipTap editor (jsdom) and verifies the merge-tag insert path
// produces a clean {{path}} token in the serialized body_html emitted to onChange.
describe('EmailTemplateBodyEditor', () => {
  it('inserts a merge tag as a {{path}} token via the variable menu', async () => {
    const onChange = vi.fn();
    render(
      <EmailTemplateBodyEditor
        initialHtml="<p>Hi </p>"
        variableGroups={[{ key: 'contact', label: 'Contact', fields: [{ path: 'contact.email', label: 'Email' }] }]}
        onChange={onChange}
      />,
    );

    // The editor mounts asynchronously; wait for the toolbar.
    const insertBtn = await screen.findByTitle('Insert merge tag');
    fireEvent.click(insertBtn);

    fireEvent.click(await screen.findByText('Email'));

    await waitFor(() => {
      const emittedCleanTag = onChange.mock.calls.some(([html]) => String(html).includes('{{contact.email}}'));
      expect(emittedCleanTag).toBe(true);
    });

    // The serialized HTML must NOT contain the editor-only chip wrapper.
    const lastHtml = String(onChange.mock.calls.at(-1)?.[0] ?? '');
    expect(lastHtml).not.toContain('data-merge-tag');
  });
});
