import { describe, it, expect } from 'vitest';
import { serializeMergeTags, deserializeMergeTags, token } from '../mergeTagHtml';

describe('serializeMergeTags', () => {
  it('rewrites a chip span with a fallback to {{path|fallback}}', () => {
    const html = `<span data-merge-tag="contact.first_name" data-merge-fallback="there" class="merge-tag">{{contact.first_name|there}}</span>`;
    expect(serializeMergeTags(html)).toBe('{{contact.first_name|there}}');
  });
  it('rewrites a chip span without a fallback to {{path}}', () => {
    const html = `<span data-merge-tag="contact.email" class="merge-tag">{{contact.email}}</span>`;
    expect(serializeMergeTags(html)).toBe('{{contact.email}}');
  });
  it('leaves chip-free HTML unchanged', () => {
    expect(serializeMergeTags('<p>Hello world</p>')).toBe('<p>Hello world</p>');
  });
});

describe('deserializeMergeTags', () => {
  it('wraps a bare token into a chip span carrying path + fallback attrs', () => {
    const out = deserializeMergeTags('Hi {{contact.first_name|there}}');
    expect(out).toContain('data-merge-tag="contact.first_name"');
    expect(out).toContain('data-merge-fallback="there"');
  });
  it('encodes special chars in the fallback attribute', () => {
    const out = deserializeMergeTags('{{x|A & B}}');
    expect(out).toContain('data-merge-fallback="A &amp; B"');
  });
});

describe('round-trip', () => {
  it('deserialize→serialize reproduces the original bare tokens', () => {
    const src = 'Hi {{contact.first_name|there}}, from {{org.name}} — code {{promo|A & B}}';
    expect(serializeMergeTags(deserializeMergeTags(src))).toBe(src);
  });
  it('is lossless for fallbacks containing raw special chars and literal entity strings', () => {
    // The decodeAttr ordering bug corrupted these; a fallback that literally reads
    // "&lt;3" must survive, and a raw "<3 & love" must too.
    for (const src of ['{{x|<3 & love}}', '{{y|&lt;3}}', '{{z|"quote" & <b>}}']) {
      expect(serializeMergeTags(deserializeMergeTags(src))).toBe(src);
    }
  });
});

describe('token', () => {
  it('formats with and without a fallback', () => {
    expect(token('a.b', 'x')).toBe('{{a.b|x}}');
    expect(token('a.b', '')).toBe('{{a.b}}');
    expect(token('a.b')).toBe('{{a.b}}');
  });
});
