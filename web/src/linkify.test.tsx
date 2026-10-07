import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { linkify } from './linkify';

describe('linkify', () => {
  it('links only http and https, renders everything else as text', () => {
    const { container } = render(<p>{linkify('see https://example.com/a?b=1. <b>bold</b> javascript:alert(1) http://x.test')}</p>);
    const links = [...container.querySelectorAll('a')].map((a) => a.getAttribute('href'));
    expect(links).toEqual(['https://example.com/a?b=1', 'http://x.test']);
    expect(container.querySelector('b')).toBeNull();
    expect(container.textContent).toContain('<b>bold</b>');
    for (const a of container.querySelectorAll('a')) {
      expect(a.getAttribute('rel')).toBe('noopener noreferrer');
      expect(a.getAttribute('target')).toBe('_blank');
    }
  });
});
