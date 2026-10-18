import { createContext, memo, useContext, useId } from 'react';
import type { ComponentPropsWithoutRef } from 'react';
import Markdown from 'react-markdown';
import type { Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import './markdown.css';

export function isMarkdownFilename(name: string): boolean {
  return /\.(?:md|markdown)$/i.test(name);
}

function safeLink(url: string): string | undefined {
  if (Array.from(url).some((character) => character.charCodeAt(0) < 32))
    return undefined;
  if (url.startsWith('#')) return url;
  if (!/^(?:https?:\/\/|mailto:)/i.test(url)) return undefined;
  try {
    const parsed = new URL(url);
    return ['http:', 'https:', 'mailto:'].includes(parsed.protocol)
      ? url
      : undefined;
  } catch {
    return undefined;
  }
}

const MarkdownScope = createContext('');

function MarkdownLink({
  href,
  children,
  title,
  id,
  'aria-label': label,
  'aria-describedby': description,
}: ComponentPropsWithoutRef<'a'>) {
  const scope = useContext(MarkdownScope);
  return href ? (
    <a
      href={href}
      title={title}
      id={id}
      aria-label={label}
      aria-describedby={description
        ?.split(/\s+/)
        .map((reference) =>
          reference === 'footnote-label' ? `${scope}footnote-label` : reference,
        )
        .join(' ')}
      target={href.startsWith('#') ? undefined : '_blank'}
      rel={href.startsWith('#') ? undefined : 'noopener noreferrer'}
    >
      {children}
    </a>
  ) : (
    <span
      className="markdown-unavailable-link"
      title="This link cannot be opened in a document preview."
    >
      {children}
    </span>
  );
}

function MarkdownSubheading({ children, id }: ComponentPropsWithoutRef<'h2'>) {
  const scope = useContext(MarkdownScope);
  return (
    <h2 id={id === 'footnote-label' ? `${scope}footnote-label` : id}>
      {children}
    </h2>
  );
}

const components: Components = {
  a: MarkdownLink,
  h2: MarkdownSubheading,
  img: ({ alt }) => (
    <span className="markdown-image-placeholder">
      [Image{alt ? `: ${alt}` : ''} — images are not loaded]
    </span>
  ),
  input: ({ checked }) => (
    <input
      type="checkbox"
      disabled
      checked={checked}
      aria-label={checked ? 'Completed task' : 'Incomplete task'}
    />
  ),
  table: ({ children }) => (
    <div className="markdown-table-scroll">
      <table>{children}</table>
    </div>
  ),
  pre: ({ children }) => <pre tabIndex={0}>{children}</pre>,
};

const allowedElements = [
  'a',
  'blockquote',
  'br',
  'code',
  'del',
  'em',
  'h1',
  'h2',
  'h3',
  'h4',
  'h5',
  'h6',
  'hr',
  'img',
  'input',
  'li',
  'ol',
  'p',
  'pre',
  'section',
  'strong',
  'sup',
  'table',
  'tbody',
  'td',
  'th',
  'thead',
  'tr',
  'ul',
];

export interface MarkdownPreviewProps {
  content: string;
  className?: string;
  emptyMessage?: string;
}

export const MAX_MARKDOWN_PREVIEW_BYTES = 64 << 10;

export const MarkdownPreview = memo(function MarkdownPreview({
  content,
  className,
  emptyMessage = 'This document is empty.',
}: MarkdownPreviewProps) {
  const id = useId();
  const scope = `axv-markdown-${id}-`;
  const large =
    content.length > MAX_MARKDOWN_PREVIEW_BYTES ||
    new TextEncoder().encode(content).byteLength > MAX_MARKDOWN_PREVIEW_BYTES;
  return (
    <div className={['markdown-preview', className].filter(Boolean).join(' ')}>
      <MarkdownScope value={scope}>
        {large ? (
          <>
            <p role="status">
              This large Markdown document is shown as source to keep the
              desktop responsive.
            </p>
            <pre tabIndex={0} aria-label="Markdown source">
              {content}
            </pre>
          </>
        ) : content.trim() ? (
          <Markdown
            skipHtml
            allowedElements={allowedElements}
            components={components}
            remarkPlugins={[remarkGfm]}
            remarkRehypeOptions={{ clobberPrefix: scope }}
            urlTransform={(url, key) =>
              key === 'href' ? safeLink(url) : undefined
            }
          >
            {content}
          </Markdown>
        ) : (
          <p className="markdown-empty">{emptyMessage}</p>
        )}
      </MarkdownScope>
    </div>
  );
});
