import request from "supertest";
import { createApp } from "../src/app";
import { Quote, QuoteConflictError, QuoteInput, QuoteRepository } from "../src/quotes";

class MemoryQuotes implements QuoteRepository {
  private readonly quotes: Quote[] = [];

  async ingest(input: QuoteInput): Promise<{ quote: Quote; created: boolean }> {
    const existing = this.quotes.find(
      (quote) => quote.provider === input.provider && quote.externalId === input.externalId,
    );
    if (existing) {
      if (
        existing.symbol !== input.symbol ||
        existing.bid !== input.bid ||
        existing.ask !== input.ask ||
        existing.last !== input.last
      ) {
        throw new QuoteConflictError("provider externalId was already used with different quote data");
      }
      return { quote: existing, created: false };
    }
    const quote: Quote = {
      id: "11111111-1111-4111-8111-111111111111",
      ...input,
      asOf: new Date(input.asOf).toISOString(),
      receivedAt: "2026-08-01T12:00:01.000Z",
    };
    this.quotes.push(quote);
    return { quote, created: true };
  }

  async latest(symbols: string[]): Promise<Quote[]> {
    return symbols.flatMap((symbol) => {
      const matches = this.quotes
        .filter((quote) => quote.symbol === symbol)
        .sort((left, right) => right.asOf.localeCompare(left.asOf));
      return matches[0] ? [matches[0]] : [];
    });
  }
}

const quote = {
  provider: "test-feed",
  externalId: "quote-1",
  symbol: "aapl",
  bid: "99.5",
  ask: "100",
  last: "99.75",
  currency: "usd",
  asOf: "2026-08-01T11:59:30.000Z",
};

describe("quote API", () => {
  it("requires provider authentication", async () => {
    const response = await request(createApp(new MemoryQuotes(), () => new Date("2026-08-01T12:00:00Z"), "secret"))
      .post("/internal/v1/quotes")
      .send(quote);
    expect(response.status).toBe(401);
  });

  it("ingests idempotently and returns freshness metadata", async () => {
    const app = createApp(new MemoryQuotes(), () => new Date("2026-08-01T12:00:00Z"), "secret", 60);
    const first = await request(app).post("/internal/v1/quotes").set("X-Market-Data-Key", "secret").send(quote);
    expect(first.status).toBe(201);
    expect(first.body.quote.symbol).toBe("AAPL");
    expect(first.body.quote.stale).toBe(false);
    expect(first.body.quote.ageSeconds).toBe(30);

    const retry = await request(app).post("/internal/v1/quotes").set("X-Market-Data-Key", "secret").send(quote);
    expect(retry.status).toBe(200);
    expect(retry.body.created).toBe(false);
  });

  it("rejects crossed markets and conflicting provider ids", async () => {
    const app = createApp(new MemoryQuotes(), () => new Date("2026-08-01T12:00:00Z"), "secret");
    const crossed = await request(app).post("/internal/v1/quotes").set("X-Market-Data-Key", "secret").send({
      ...quote,
      bid: "101",
      ask: "100",
    });
    expect(crossed.status).toBe(400);

    await request(app).post("/internal/v1/quotes").set("X-Market-Data-Key", "secret").send(quote);
    const conflict = await request(app).post("/internal/v1/quotes").set("X-Market-Data-Key", "secret").send({
      ...quote,
      last: "98",
    });
    expect(conflict.status).toBe(409);
  });

  it("returns latest quotes, missing symbols, and stale status", async () => {
    const app = createApp(new MemoryQuotes(), () => new Date("2026-08-01T12:02:00Z"), "secret", 60);
    await request(app).post("/internal/v1/quotes").set("X-Market-Data-Key", "secret").send(quote);
    const response = await request(app).get("/api/v1/quotes?symbols=AAPL,MSFT");
    expect(response.status).toBe(200);
    expect(response.body.quotes[0].stale).toBe(true);
    expect(response.body.missingSymbols).toEqual(["MSFT"]);
  });
});
