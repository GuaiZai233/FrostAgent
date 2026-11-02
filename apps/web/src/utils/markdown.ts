import createDOMPurify from 'dompurify';
import { Marked } from 'marked';
import { escapeHtml } from './formatters';

const markdown = new Marked({
  gfm: true,
  breaks: true,
  renderer: {
    html: ({ text }) => escapeHtml(text),
    // Prompt previews must not fetch embedded resources automatically.
    image: ({ text }) => escapeHtml(text),
  },
});

let purifier: ReturnType<typeof createDOMPurify> | undefined;

export function renderMarkdown(source: string): string {
  purifier ??= createDOMPurify(window);
  return purifier.sanitize(markdown.parse(source, { async: false }), {
    ALLOWED_TAGS: [
      'p', 'br', 'hr', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
      'strong', 'em', 'del', 'blockquote', 'pre', 'code',
      'ul', 'ol', 'li', 'a', 'table', 'thead', 'tbody', 'tr', 'th', 'td',
    ],
    ALLOWED_ATTR: ['href', 'title', 'start', 'align'],
    ALLOW_DATA_ATTR: false,
    ALLOW_ARIA_ATTR: false,
    RETURN_TRUSTED_TYPE: false,
  });
}
