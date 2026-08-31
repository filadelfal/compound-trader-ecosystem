import { CandleAggregator, type CandleStore, type CandleUpdate } from "./candles";
import { enrichQuote, quoteInputSchema, type QuoteStore, type StoredQuote } from "./quotes";

export interface IngestLimits {
  maxQuoteAgeSeconds: number;
  maxQuoteFutureSkewSeconds: number;
  maxSpreadPips: number;
}

export type IngestResult =
  | {
      ok: true;
      status: "accepted" | "duplicate";
      quote: StoredQuote;
      candleUpdates: CandleUpdate[];
    }
  | {
      ok: false;
      status:
        | "invalid_quote"
        | "stale_quote"
        | "future_quote"
        | "spread_too_wide"
        | "out_of_order"
        | "conflict"
        | "quote_store_unavailable"
        | "candle_store_unavailable";
      httpStatus: 400 | 409 | 422 | 503;
      details?: unknown;
      spreadPips?: number;
    };

export class QuoteIngestor {
  private readonly candleAggregator: CandleAggregator;

  constructor(
    private readonly quoteStore: QuoteStore,
    candleStore: CandleStore,
    private readonly limits: IngestLimits,
    private readonly now: () => Date = () => new Date(),
  ) {
    this.candleAggregator = new CandleAggregator(candleStore);
  }

  async ingest(input: unknown): Promise<IngestResult> {
    const parsed = quoteInputSchema.safeParse(input);
    if (!parsed.success) {
      return {
        ok: false,
        status: "invalid_quote",
        httpStatus: 400,
        details: parsed.error.flatten(),
      };
    }

    const receivedAt = this.now();
    const ageMilliseconds = receivedAt.getTime() - Date.parse(parsed.data.observedAt);
    if (ageMilliseconds > this.limits.maxQuoteAgeSeconds * 1000) {
      return { ok: false, status: "stale_quote", httpStatus: 422 };
    }
    if (ageMilliseconds < -this.limits.maxQuoteFutureSkewSeconds * 1000) {
      return { ok: false, status: "future_quote", httpStatus: 422 };
    }

    const quote = enrichQuote(parsed.data, receivedAt);
    if (quote.spreadPips > this.limits.maxSpreadPips) {
      return {
        ok: false,
        status: "spread_too_wide",
        httpStatus: 422,
        spreadPips: quote.spreadPips,
      };
    }

    let result;
    try {
      result = await this.quoteStore.put(quote);
    } catch {
      return { ok: false, status: "quote_store_unavailable", httpStatus: 503 };
    }

    if (result === "out_of_order" || result === "conflict") {
      return { ok: false, status: result, httpStatus: 409 };
    }

    let candleUpdates: CandleUpdate[] = [];
    try {
      candleUpdates = await this.candleAggregator.process(quote);
    } catch {
      return { ok: false, status: "candle_store_unavailable", httpStatus: 503 };
    }

    return { ok: true, status: result, quote, candleUpdates };
  }
}
