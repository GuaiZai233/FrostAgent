type MemoryDisplayEntry = {
  content: string;
  summary?: string;
  evidence?: string;
  source: string;
  sourceMedia?: string;
};

export function memoryDisplayContent(entry: MemoryDisplayEntry): string {
  // Manual revisions take precedence over the original extraction summary.
  return entry.source === 'manual'
    ? entry.content
    : entry.summary?.trim() || entry.content;
}

export function memoryEvidenceText(entry: MemoryDisplayEntry): string {
  return entry.sourceMedia === 'image'
    ? '（原source为图片形式）'
    : entry.evidence || '';
}
