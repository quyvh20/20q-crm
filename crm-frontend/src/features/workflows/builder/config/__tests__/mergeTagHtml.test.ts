import { describe, it, expect } from 'vitest';
import { serializeMergeTags } from '../mergeTagHtml';

describe('serializeMergeTags', () => {
  it('unwraps a chip span into a bare {{path}} token', () => {
    const html = '<p>Hi <span data-merge-tag="contact.first_name" class="merge-tag">{{contact.first_name}}</span>!</p>';
    expect(serializeMergeTags(html)).toBe('<p>Hi {{contact.first_name}}!</p>');
  });

  it('unwraps multiple chips', () => {
    const html =
      '<p><span data-merge-tag="contact.first_name" class="merge-tag">{{contact.first_name}}</span> ' +
      '<span class="merge-tag" data-merge-tag="deal.title">{{deal.title}}</span></p>';
    expect(serializeMergeTags(html)).toBe('<p>{{contact.first_name}} {{deal.title}}</p>');
  });

  it('handles attribute order (data-merge-tag after class)', () => {
    const html = '<span class="merge-tag" data-merge-tag="deal.value">{{deal.value}}</span>';
    expect(serializeMergeTags(html)).toBe('{{deal.value}}');
  });

  it('leaves HTML without chips unchanged', () => {
    const html = '<p>Plain <strong>body</strong> with {{contact.email}} literal text</p>';
    expect(serializeMergeTags(html)).toBe(html);
  });

  it('decodes entity-encoded paths', () => {
    const html = '<span data-merge-tag="custom_fields.a&amp;b">{{custom_fields.a&amp;b}}</span>';
    expect(serializeMergeTags(html)).toBe('{{custom_fields.a&b}}');
  });
});
