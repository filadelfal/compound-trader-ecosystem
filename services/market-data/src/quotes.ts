import { z } from "zod";

const supportedSymbols = ["EURUSD", "GBPUSD", "USDJPY"] as const;

export const quoteInputSchema = z.object({
  provider: z.string().trim().min(1).max(64),
  symbol: z.string().trim().transform((value) => value.toUpperCase()).pipe(z.enum(supportedSymbols)),
  bid: z.number().positive().finite(),
  ask: z.number().positive().finite(),
  observedAt: z.string().datetime({ offset: true }),
}).strict().superRefine((quote, context) => {
  if (quote.ask <= quote.bid) {
    context.addIssue({ code: z.ZodIssueCode.custom, path: ["ask"], message: "ask must be greater than bid" });
  }
});

export const storedQuoteSchema = z.object({
  provider: z.string().trim().min(1).max(64),
  symbol: z.string().trim().transform((value) => value.toUpperCase()).pipe(z.enum(supportedSymbols)),
  bid: z.number().positive().finite(),
  ask: z.number().positive().finite(),
  observedAt: z.string().datetime({ offset: true }),
  receivedAt: z.string().datetime({ offset: true }),
  spread: z.number().nonnegative().finite(),
  spreadPips: z.number().nonnegative().finite(),
}).strict().superRefine((quote, context) => {
  if (quote.ask <= quote.bid) {
    context.addIssue({
      code: z.ZodIssueCode.custom,
      path: ["ask"],
      message: "ask must be greater than bid",
    });
  }
});

export type QuoteInput = z.infer<typeof quoteInputSchema>;
export type StoredQuote = z.infer<typeof storedQuoteSchema>;
export type PutResult = "accepted" | "duplicate" | "out_of_order" | "conflict";

export interface QuoteStore {
  put(quote: StoredQuote): Promise<PutResult>;
  latest(symbol: string): Promise<StoredQuote | undefined>;
}

interface KeyValueClient {
  get(key: string): Promise<string | null>;
  set(key: string, value: string): Promise<unknown>;
}

export function pipSize(symbol: string): number {
  return symbol.endsWith("JPY") ? 0.01 : 0.0001;
}

export function enrichQuote(quote: QuoteInput, receivedAt: Date): StoredQuote {
  const spread = quote.ask - quote.bid;
  return {
    ...quote,
    receivedAt: receivedAt.toISOString(),
    spread,
    spreadPips: spread / pipSize(quote.symbol),
  };
}

function compareQuote(current: StoredQuote, incoming: StoredQuote): PutResult | undefined {
  const incomingTime = Date.parse(incoming.observedAt);
  const currentTime = Date.parse(current.observedAt);
  if (incomingTime < currentTime) return "out_of_order";
  if (incomingTime === currentTime) {
    if (
      incoming.provider === current.provider &&
      incoming.bid === current.bid &&
      incoming.ask === current.ask
    ) {
      return "duplicate";
    }
    return "conflict";
  }
  return undefined;
}

export class InMemoryQuoteStore implements QuoteStore {
  private readonly quotes = new Map<string, StoredQuote>();

  async put(quote: StoredQuote): Promise<PutResult> {
    const current = this.quotes.get(quote.symbol);
    if (current) {
      const result = compareQuote(current, quote);
      if (result) return result;
    }
    this.quotes.set(quote.symbol, Object.freeze({ ...quote }));
    return "accepted";
  }

  async latest(symbol: string): Promise<StoredQuote | undefined> {
    return this.quotes.get(symbol.toUpperCase());
  }
}

export class RedisQuoteStore implements QuoteStore {
  constructor(
    private readonly client: KeyValueClient,
    private readonly keyPrefix = "market-data:latest-quote",
  ) {}

  private key(symbol: string): string {
    return `${this.keyPrefix}:${symbol.toUpperCase()}`;
  }

  async put(quote: StoredQuote): Promise<PutResult> {
    const current = await this.latest(quote.symbol);
    if (current) {
      const result = compareQuote(current, quote);
      if (result) return result;
    }
    await this.client.set(this.key(quote.symbol), JSON.stringify(quote));
    return "accepted";
  }

  async latest(symbol: string): Promise<StoredQuote | undefined> {
    const raw = await this.client.get(this.key(symbol));
    if (!raw) return undefined;
    return storedQuoteSchema.parse(JSON.parse(raw));
  }
}
