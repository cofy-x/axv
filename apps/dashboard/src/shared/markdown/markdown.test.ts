import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import {
  isMarkdownFilename,
  MarkdownPreview,
  MAX_MARKDOWN_PREVIEW_BYTES,
} from './index';

function render(content: string) {
  return renderToStaticMarkup(createElement(MarkdownPreview, { content }));
}

describe('Markdown preview', () => {
  it('recognizes Markdown extensions without treating other text as Markdown', () => {
    for (const name of ['Plan.md', 'README.MD', '说明.Markdown'])
      expect(isMarkdownFilename(name)).toBe(true);
    for (const name of ['Plan.txt', 'markdown', 'Plan.md.txt', 'md'])
      expect(isMarkdownFilename(name)).toBe(false);
  });

  it('renders readable CommonMark and GFM without changing source text', () => {
    const content = [
      '# 工作计划',
      '',
      '**Important** and ~~obsolete~~.',
      '',
      '- [x] Complete',
      '- [ ] Pending',
      '',
      '| Name | State |',
      '| --- | --- |',
      '| AXV | Ready |',
      '',
      '```js',
      'const value = "<safe>";',
      '```',
    ].join('\n');
    const result = render(content);
    expect(result).toContain('<h1>工作计划</h1>');
    expect(result).toContain('<strong>Important</strong>');
    expect(result).toContain('<del>obsolete</del>');
    expect(result).toContain('<table>');
    expect(result).toContain('aria-label="Completed task"');
    expect(result).toContain('aria-label="Incomplete task"');
    expect(result).toContain('disabled=""');
    expect(result).toContain('&lt;safe&gt;');
    expect(content).toContain('"<safe>"');
  });

  it('never renders raw HTML, script links or fetchable images', () => {
    const result = render(
      [
        '<script>alert(1)</script>',
        '',
        '<img src="https://example.com/tracker" onerror="alert(2)">',
        '',
        '[script](javascript:alert%281%29)',
        '[encoded](java&#x73;cript:alert%281%29)',
        '[data](data:text/html,attack)',
        '[file](file:///etc/passwd)',
        '',
        '![Remote](https://example.com/tracker.png)',
        '![Relative](./image.png)',
        '![Embedded](data:image/svg+xml,attack)',
      ].join('\n'),
    );
    expect(result).not.toMatch(/<(?:script|img)\b/);
    expect(result).not.toContain('onerror');
    expect(result).not.toContain('href=');
    expect(result).not.toContain('src=');
    expect(result).toContain('[Image: Remote — images are not loaded]');
    expect(result).toContain('[Image: Relative — images are not loaded]');
  });

  it('allows explicit web and email links while keeping relative routes inert', () => {
    const result = render(
      [
        '[Site](https://example.com/guide)',
        '[Email](mailto:hello@example.com)',
        '[Relative](./guide.md)',
        '[Server](/api/entries)',
        '[Protocol relative](//example.com)',
      ].join('\n'),
    );
    expect(result).toContain('href="https://example.com/guide"');
    expect(result).toContain('href="mailto:hello@example.com"');
    expect(result.match(/target="_blank"/g)).toHaveLength(2);
    expect(result.match(/rel="noopener noreferrer"/g)).toHaveLength(2);
    expect(result).not.toContain('href="./guide.md"');
    expect(result).not.toContain('href="/api/entries"');
    expect(result).not.toContain('href="//example.com"');
  });

  it('keeps footnote references within a uniquely scoped preview', () => {
    const content = 'A note[^1].\n\n[^1]: Explanation.';
    const result = renderToStaticMarkup(
      createElement(
        'div',
        null,
        createElement(MarkdownPreview, { content }),
        createElement(MarkdownPreview, { content }),
      ),
    );
    const ids = Array.from(
      result.matchAll(/ id="([^"]+)"/g),
      (match) => match[1],
    );
    const references = Array.from(
      result.matchAll(/ href="#([^"]+)"/g),
      (match) => match[1],
    );
    expect(ids.length).toBeGreaterThan(0);
    expect(new Set(ids).size).toBe(ids.length);
    for (const reference of references) expect(ids).toContain(reference);
  });

  it('explains an empty document', () => {
    expect(render('  \n')).toContain('This document is empty.');
  });

  it('keeps large source readable without parsing an unbounded Markdown tree', () => {
    const source =
      '# Large document\n' +
      '界'.repeat(Math.ceil(MAX_MARKDOWN_PREVIEW_BYTES / 3));
    const result = render(source);
    expect(result).toContain('shown as source');
    expect(result).toContain('aria-label="Markdown source"');
    expect(result).toContain('tabindex="0"');
    expect(result).toContain('># Large document');
    expect(result).not.toContain('<h1>');
    expect(result).toContain(source);
  });
});
