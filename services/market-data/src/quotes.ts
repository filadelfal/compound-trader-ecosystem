import { z } from "zod";

const decimal = z.string().regex(/^(0|[1-9][0-9]*)(\.[0-9]{1,8})?$/);
const symbol = z.string().trim().min(1).max(20).regex(/^[A-Za-z0-9._-]+$/);

export const quoteInputSchema = z.object({
  provider: z.string().trim().min(1).max(50),
  externalId: z.string().trim().min(1).max(100),
  symbol,
  bid: decimal,
  ask: decimal,
  last: decimal,
  currency: z.string().trim().length(3).regex(/^[A-Za-z]+$/).default("USD"),
  asOf: z.string().datetime({ offset: true }),
});

export type QuoteInput = z.infer<typeof quoteInputSchema>;

export interface Quote {
  id: string;
  provider: string;
  externalId: string;
  symbol: string;
  bid: string;
  ask: string;
  last: string;
  currency: string;
  asOf: string;
  receivedAt: string;
}

export interface QuoteView extends Quote {
  ageSeconds: number;
  stale: boolean;
}

export interface QuoteRepository {
  ingest(input: QuoteInput): Promise<{ quote: Quote; created: boolean }>;
  latest(symbols: string[]): Promise<Quote[]>;
}

export class QuoteConflictError extends Error {}

export class QuoteService {
  constructor(
    private readonly repository: QuoteRepository,
    private readonly staleAfterSeconds: number,
    private readonly now: () => Date = () => new Date(),
  ) {}

  async ingest(raw: unknown): Promise<{ quote: QuoteView; created: boolean }> {
    const input = quoteInputSchema.parse(raw);
    input.symbol = input.symbol.toUpperCase();
    input.currency = input.currency.toUpperCase();
    if (toScaled(input.bid) > toScaled(input.ask)) {
      throw new Error("bid must not exceed ask");
    }
    const result = await this.repository.ingest(input);
    return { quote: this.view(result.quote), created: result.created };
  }

  async latest(rawSymbols: string[]): Promise<QuoteView[]> {
    const symbols = [...new Set(rawSymbols.map((value) => symbol.parse(value).toUpperCase()))];
    if (symbols.length < 1 || symbols.length > 100) {
      throw new Error("between 1 and 100 symbols are required");
    }
    const quotes = await this.repository.latest(symbols);
    return quotes.map((quote) => this.view(quote));
  }

  private view(quote: Quote): QuoteView {
    const ageSeconds = Math.max(0, Math.floor((this.now().getTime() - new Date(quote.asOf).getTime()) / 1000));
    return { ...quote, ageSeconds, stale: ageSeconds > this.staleAfterSeconds };
  }
}

function toScaled(value: string): bigint {
  const [whole, fraction = ""] = value.split(".");
  return BigInt(whole) * 100_000_000n + BigInt(fraction.padEnd(8, "0"));
}
