import { z } from "zod";
import type { StoredQuote } from "./quotes";

export const timeframeSchema = z.enum(["H1", "H4"]);
export type Timeframe = z.infer<typeof timeframeSchema>;

export interface Candle {
  symbol: StoredQuote["symbol"];
  timeframe: Timeframe;
  openTime: string;
  closeTime: string;
  open: number;
  high: number;
  low: number;
  close: number;
  quoteCount: number;
  lastQuoteKey: string;
  status: "OPEN" | "CLOSED";
}

export interface CandleGap {
  symbol: StoredQuote["symbol"];
  timeframe: Timeframe;
  missingOpenTime: string;
  detectedAt: string;
}

export interface CandleUpdate {
  current: Candle;
  closed?: Candle;
  gaps: CandleGap[];
}

export interface CandleStore {
  current(symbol: string, timeframe: Timeframe): Promise<Candle | undefined>;
  saveCurrent(candle: Candle): Promise<void>;
  appendClosed(candle: Candle): Promise<void>;
  appendGaps(gaps: CandleGap[]): Promise<void>;
  closed(symbol: string, timeframe: Timeframe, limit: number): Promise<Candle[]>;
  gaps(symbol: string, timeframe: Timeframe, limit: number): Promise<CandleGap[]>;
}

interface RedisListClient {
  get(key: string): Promise<string | null>;
  set(key: string, value: string): Promise<unknown>;
  rPush(key: string, value: string | string[]): Promise<number>;
  lRange(key: string, start: number, stop: number): Promise<string[]>;
  lTrim(key: string, start: number, stop: number): Promise<string>;
}

const milliseconds: Record<Timeframe, number> = {
  H1: 60 * 60 * 1000,
  H4: 4 * 60 * 60 * 1000,
};

export function candleOpenTime(observedAt: string, timeframe: Timeframe): Date {
  const duration = milliseconds[timeframe];
  const timestamp = Date.parse(observedAt);
  return new Date(Math.floor(timestamp / duration) * duration);
}

function newCandle(quote: StoredQuote, timeframe: Timeframe, openTime: Date): Candle {
  const duration = milliseconds[timeframe];
  const price = (quote.bid + quote.ask) / 2;
  return {
    symbol: quote.symbol,
    timeframe,
    openTime: openTime.toISOString(),
    closeTime: new Date(openTime.getTime() + duration).toISOString(),
    open: price,
    high: price,
    low: price,
    close: price,
    quoteCount: 1,
    lastQuoteKey: quoteKey(quote),
    status: "OPEN",
  };
}

function quoteKey(quote: StoredQuote): string {
  return `${quote.provider}|${quote.observedAt}|${quote.bid}|${quote.ask}`;
}

export class CandleAggregator {
  constructor(private readonly store: CandleStore) {}

  async process(quote: StoredQuote): Promise<CandleUpdate[]> {
    return Promise.all((["H1", "H4"] as const).map((timeframe) => this.processTimeframe(quote, timeframe)));
  }

  private async processTimeframe(quote: StoredQuote, timeframe: Timeframe): Promise<CandleUpdate> {
    const bucket = candleOpenTime(quote.observedAt, timeframe);
    const existing = await this.store.current(quote.symbol, timeframe);
    if (!existing) {
      const current = newCandle(quote, timeframe, bucket);
      await this.store.saveCurrent(current);
      return { current, gaps: [] };
    }

    const existingOpen = Date.parse(existing.openTime);
    if (bucket.getTime() === existingOpen) {
      if (existing.lastQuoteKey === quoteKey(quote)) {
        return { current: existing, gaps: [] };
      }
      const price = (quote.bid + quote.ask) / 2;
      const current: Candle = {
        ...existing,
        high: Math.max(existing.high, price),
        low: Math.min(existing.low, price),
        close: price,
        quoteCount: existing.quoteCount + 1,
        lastQuoteKey: quoteKey(quote),
      };
      await this.store.saveCurrent(current);
      return { current, gaps: [] };
    }

    if (bucket.getTime() < existingOpen) {
      return { current: existing, gaps: [] };
    }

    const closed: Candle = { ...existing, status: "CLOSED" };
    const gaps: CandleGap[] = [];
    const duration = milliseconds[timeframe];
    for (let missing = existingOpen + duration; missing < bucket.getTime(); missing += duration) {
      gaps.push({
        symbol: quote.symbol,
        timeframe,
        missingOpenTime: new Date(missing).toISOString(),
        detectedAt: quote.receivedAt,
      });
    }
    const current = newCandle(quote, timeframe, bucket);
    await this.store.appendClosed(closed);
    await this.store.appendGaps(gaps);
    await this.store.saveCurrent(current);
    return { current, closed, gaps };
  }
}

export class InMemoryCandleStore implements CandleStore {
  private readonly currentCandles = new Map<string, Candle>();
  private readonly closedCandles = new Map<string, Candle[]>();
  private readonly gapRecords = new Map<string, CandleGap[]>();

  private key(symbol: string, timeframe: Timeframe): string {
    return `${symbol.toUpperCase()}:${timeframe}`;
  }

  async current(symbol: string, timeframe: Timeframe): Promise<Candle | undefined> {
    return this.currentCandles.get(this.key(symbol, timeframe));
  }

  async saveCurrent(candle: Candle): Promise<void> {
    this.currentCandles.set(this.key(candle.symbol, candle.timeframe), { ...candle });
  }

  async appendClosed(candle: Candle): Promise<void> {
    const key = this.key(candle.symbol, candle.timeframe);
    this.closedCandles.set(key, [...(this.closedCandles.get(key) ?? []), { ...candle }]);
  }

  async appendGaps(gaps: CandleGap[]): Promise<void> {
    for (const gap of gaps) {
      const key = this.key(gap.symbol, gap.timeframe);
      this.gapRecords.set(key, [...(this.gapRecords.get(key) ?? []), { ...gap }]);
    }
  }

  async closed(symbol: string, timeframe: Timeframe, limit: number): Promise<Candle[]> {
    return (this.closedCandles.get(this.key(symbol, timeframe)) ?? []).slice(-limit);
  }

  async gaps(symbol: string, timeframe: Timeframe, limit: number): Promise<CandleGap[]> {
    return (this.gapRecords.get(this.key(symbol, timeframe)) ?? []).slice(-limit);
  }
}

export class RedisCandleStore implements CandleStore {
  constructor(private readonly client: RedisListClient, private readonly retention = 1000) {}

  private key(kind: "current" | "closed" | "gaps", symbol: string, timeframe: Timeframe): string {
    return `market-data:candles:${kind}:${symbol.toUpperCase()}:${timeframe}`;
  }

  async current(symbol: string, timeframe: Timeframe): Promise<Candle | undefined> {
    const raw = await this.client.get(this.key("current", symbol, timeframe));
    return raw ? JSON.parse(raw) as Candle : undefined;
  }

  async saveCurrent(candle: Candle): Promise<void> {
    await this.client.set(this.key("current", candle.symbol, candle.timeframe), JSON.stringify(candle));
  }

  async appendClosed(candle: Candle): Promise<void> {
    const key = this.key("closed", candle.symbol, candle.timeframe);
    await this.client.rPush(key, JSON.stringify(candle));
    await this.client.lTrim(key, -this.retention, -1);
  }

  async appendGaps(gaps: CandleGap[]): Promise<void> {
    if (gaps.length === 0) return;
    const grouped = new Map<string, CandleGap[]>();
    for (const gap of gaps) {
      const key = this.key("gaps", gap.symbol, gap.timeframe);
      grouped.set(key, [...(grouped.get(key) ?? []), gap]);
    }
    for (const [key, records] of grouped) {
      await this.client.rPush(key, records.map((record) => JSON.stringify(record)));
      await this.client.lTrim(key, -this.retention, -1);
    }
  }

  async closed(symbol: string, timeframe: Timeframe, limit: number): Promise<Candle[]> {
    return (await this.client.lRange(this.key("closed", symbol, timeframe), -limit, -1))
      .map((raw) => JSON.parse(raw) as Candle);
  }

  async gaps(symbol: string, timeframe: Timeframe, limit: number): Promise<CandleGap[]> {
    return (await this.client.lRange(this.key("gaps", symbol, timeframe), -limit, -1))
      .map((raw) => JSON.parse(raw) as CandleGap);
  }
}
