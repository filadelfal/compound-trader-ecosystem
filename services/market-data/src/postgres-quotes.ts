import { pool } from "./db";
import { Quote, QuoteConflictError, QuoteInput, QuoteRepository } from "./quotes";

const columns = `id::text, provider, external_id AS "externalId", symbol,
  bid::text, ask::text, last::text, currency, as_of AS "asOf",
  received_at AS "receivedAt"`;

export class PostgresQuoteRepository implements QuoteRepository {
  async ingest(input: QuoteInput): Promise<{ quote: Quote; created: boolean }> {
    const inserted = await pool.query<Quote>(
      `INSERT INTO market_quotes
        (provider, external_id, symbol, bid, ask, last, currency, as_of)
       VALUES ($1, $2, $3, $4::numeric, $5::numeric, $6::numeric, $7, $8::timestamptz)
       ON CONFLICT (provider, external_id) DO NOTHING
       RETURNING ${columns}`,
      [input.provider, input.externalId, input.symbol, input.bid, input.ask, input.last, input.currency, input.asOf],
    );
    if (inserted.rows[0]) {
      return { quote: normalizeQuote(inserted.rows[0]), created: true };
    }
    const existing = await pool.query<Quote>(
      `SELECT ${columns} FROM market_quotes WHERE provider = $1 AND external_id = $2`,
      [input.provider, input.externalId],
    );
    const quote = normalizeQuote(existing.rows[0]);
    if (
      quote.symbol !== input.symbol ||
      quote.bid !== normalizeDecimal(input.bid) ||
      quote.ask !== normalizeDecimal(input.ask) ||
      quote.last !== normalizeDecimal(input.last) ||
      quote.currency !== input.currency ||
      new Date(quote.asOf).getTime() !== new Date(input.asOf).getTime()
    ) {
      throw new QuoteConflictError("provider externalId was already used with different quote data");
    }
    return { quote, created: false };
  }

  async latest(symbols: string[]): Promise<Quote[]> {
    const result = await pool.query<Quote>(
      `SELECT DISTINCT ON (symbol) ${columns}
       FROM market_quotes
       WHERE symbol = ANY($1::text[])
       ORDER BY symbol, as_of DESC, received_at DESC, id DESC`,
      [symbols],
    );
    return result.rows.map(normalizeQuote);
  }
}

function normalizeQuote(row: Quote): Quote {
  return {
    ...row,
    bid: normalizeDecimal(row.bid),
    ask: normalizeDecimal(row.ask),
    last: normalizeDecimal(row.last),
    asOf: new Date(row.asOf).toISOString(),
    receivedAt: new Date(row.receivedAt).toISOString(),
  };
}

function normalizeDecimal(value: string): string {
  const [whole, fraction = ""] = value.split(".");
  const trimmed = fraction.replace(/0+$/, "");
  return trimmed ? `${whole}.${trimmed}` : whole;
}
