import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import MessageBubble from '../MessageBubble';
import type { ChatMessage } from '../chatTypes';

// MessageBubble swapped react-syntax-highlighter's `Prism` (which bundles
// refractor/all — every Prism grammar, ~575 kB raw and the single largest
// package in the app) for `PrismLight` plus an explicit language subset.
//
// That trade is only safe if two things hold, and neither is visible from a
// bundle report: a language IN the subset must still highlight, and a language
// OUTSIDE it must degrade to plain text rather than throwing. Both are pinned
// here, because the failure mode of getting the second one wrong is a crashed
// AI answer whenever the model happens to tag a fence with something exotic.

function assistant(content: string): ChatMessage {
  return { role: 'assistant', content } as ChatMessage;
}

describe('MessageBubble code blocks', () => {
  it('highlights a language that is registered in the subset', () => {
    render(<MessageBubble message={assistant('```sql\nSELECT 1 FROM contacts;\n```')} />);

    const code = screen.getByText(/FROM/i, { selector: 'span' });
    expect(code).toBeInTheDocument();
    // Highlighted output is a tree of <span class="token ..."> nodes. Plain,
    // unhighlighted output would be a single text node with no token classes.
    const block = code.closest('pre, div[class*="language"], code') ?? code.parentElement!;
    expect(block.querySelectorAll('span.token').length).toBeGreaterThan(0);
  });

  it('renders an unregistered language as plain text instead of throwing', () => {
    // 'brainfuck' is a real Prism grammar that is deliberately NOT registered.
    // react-syntax-highlighter checks `astGenerator.registered(language)` first
    // and falls back to unhighlighted text, so the answer still renders.
    let container!: HTMLElement;
    expect(() => {
      ({ container } = render(<MessageBubble message={assistant('```brainfuck\n++++[>++<-]\n```')} />));
    }).not.toThrow();

    // textContent rather than getByText: the highlighter wraps output in nested
    // spans even on the plain-text path, so the source is split across nodes.
    expect(container.textContent).toContain('++++[>++<-]');
    // The header still names the language, so the user sees what was intended.
    expect(screen.getByText('brainfuck')).toBeInTheDocument();
  });

  it('leaves inline code alone', () => {
    render(<MessageBubble message={assistant('Use the `contacts` table.')} />);

    const inline = screen.getByText('contacts');
    expect(inline.tagName).toBe('CODE');
    // No copy affordance: that belongs to fenced blocks only.
    expect(screen.queryByTitle('Copy code')).not.toBeInTheDocument();
  });
});
