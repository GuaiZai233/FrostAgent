import type { StickerItem } from '@frostagent/proto';

interface StickerBatch {
  stickers: StickerItem[];
  nextPageToken: string;
  totalCount: number;
}

export type StickerFeedChange =
  | { kind: 'reset' }
  | { kind: 'append'; stickers: StickerItem[] }
  | { kind: 'status' };

// Keep a slow or stale page request from replacing an already visible feed.
export class StickerFeed {
  stickers: StickerItem[] = [];
  total = 0;
  loading = false;
  error = '';
  private nextToken = '';
  private loaded = false;
  private generation = 0;
  private disposed = false;
  private readonly loadedTokens = new Set<string>();

  constructor(
    private readonly fetchBatch: (token: string) => Promise<StickerBatch>,
    private readonly onChange: (change: StickerFeedChange) => void,
  ) {}

  get hasMore(): boolean {
    return !this.loaded || this.nextToken !== '';
  }

  async reset(): Promise<void> {
    if (this.disposed) return;
    this.generation++;
    this.stickers = [];
    this.total = 0;
    this.nextToken = '';
    this.loaded = false;
    this.loading = false;
    this.error = '';
    this.loadedTokens.clear();
    this.onChange({ kind: 'reset' });
    await this.loadMore();
  }

  async loadMore(): Promise<void> {
    if (this.disposed || this.loading || !this.hasMore) return;
    const generation = this.generation;
    const token = this.nextToken;
    this.loading = true;
    this.error = '';
    this.onChange({ kind: 'status' });
    try {
      const batch = await this.fetchBatch(token);
      if (this.disposed || generation !== this.generation) return;
      const knownIDs = new Set(this.stickers.map((sticker) => sticker.id));
      const added = batch.stickers.filter((sticker) => {
        if (knownIDs.has(sticker.id)) return false;
        knownIDs.add(sticker.id);
        return true;
      });
      this.stickers.push(...added);
      this.total = batch.totalCount;
      this.loaded = true;
      this.loadedTokens.add(token);
      // Avoid an endless automatic loop if the server repeats a cursor.
      this.nextToken = this.loadedTokens.has(batch.nextPageToken)
        ? ''
        : batch.nextPageToken;
      this.onChange({ kind: 'append', stickers: added });
    } catch (error) {
      if (this.disposed || generation !== this.generation) return;
      this.error = error instanceof Error ? error.message : String(error);
    } finally {
      if (!this.disposed && generation === this.generation) {
        this.loading = false;
        this.onChange({ kind: 'status' });
      }
    }
  }

  dispose(): void {
    this.disposed = true;
    this.generation++;
  }
}
