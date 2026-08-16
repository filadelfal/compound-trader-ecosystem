import request from "supertest";
import { createApp } from "../src/app";
import { InMemoryQuoteStore } from "../src/quotes";
import { InMemoryCandleStore } from "../src/candles";

function quote(observedAt: string, bid: number, ask: number) {
  return { provider: "test-provider", symbol: "EURUSD", bid, ask, observedAt };
}

describe("H1/H4 candle construction", () => {
  it("aggregates midpoint OHLC values in aligned UTC buckets", async () => {
    let receivedAt = "2026-08-01T11:00:02.000Z";
    const clock = () => new Date(receivedAt);
    const candles = new InMemoryCandleStore();
    const app = createApp(new InMemoryQuoteStore(), clock, candles);
    await request(app).post("/api/v1/quotes").send(quote("2026-08-01T11:00:00.000Z", 1.1000, 1.1002));
    receivedAt = "2026-08-01T11:30:02.000Z";
    await request(app).post("/api/v1/quotes").send(quote("2026-08-01T11:30:00.000Z", 1.1010, 1.1012));

    const h1 = await request(app).get("/api/v1/candles/EURUSD/H1");
    expect(h1.status).toBe(200);
    expect(h1.body.current).toMatchObject({
      openTime: "2026-08-01T11:00:00.000Z",
      open: 1.1001,
      high: 1.1011,
      low: 1.1001,
      close: 1.1011,
      quoteCount: 2,
      status: "OPEN",
    });

    const h4 = await request(app).get("/api/v1/candles/EURUSD/H4");
    expect(h4.body.current.openTime).toBe("2026-08-01T08:00:00.000Z");
  });

  it("closes prior candles and persists detected missing intervals", async () => {
    let receivedAt = "2026-08-01T08:00:02.000Z";
    const clock = () => new Date(receivedAt);
    const candles = new InMemoryCandleStore();
    const app = createApp(new InMemoryQuoteStore(), clock, candles);
    await request(app).post("/api/v1/quotes").send(quote("2026-08-01T08:00:00.000Z", 1.1000, 1.1002));
    receivedAt = "2026-08-01T11:00:02.000Z";
    await request(app).post("/api/v1/quotes").send(quote("2026-08-01T11:00:00.000Z", 1.1010, 1.1012));

    const h1 = await request(app).get("/api/v1/candles/EURUSD/H1");
    expect(h1.body.closed).toHaveLength(1);
    expect(h1.body.closed[0].status).toBe("CLOSED");
    expect(h1.body.gaps.map((gap: { missingOpenTime: string }) => gap.missingOpenTime)).toEqual([
      "2026-08-01T09:00:00.000Z",
      "2026-08-01T10:00:00.000Z",
    ]);
  });

  it("does not double-count a retried provider event", async () => {
    const clock = () => new Date("2026-08-01T11:00:02.000Z");
    const candles = new InMemoryCandleStore();
    const app = createApp(new InMemoryQuoteStore(), clock, candles);
    const event = quote("2026-08-01T11:00:00.000Z", 1.1000, 1.1002);
    expect((await request(app).post("/api/v1/quotes").send(event)).status).toBe(202);
    expect((await request(app).post("/api/v1/quotes").send(event)).status).toBe(200);

    const h1 = await request(app).get("/api/v1/candles/EURUSD/H1");
    expect(h1.body.current.quoteCount).toBe(1);
  });

  it("rejects invalid candle queries", async () => {
    const clock = () => new Date("2026-08-01T12:00:02.000Z");
    const app = createApp(new InMemoryQuoteStore(), clock, new InMemoryCandleStore());
    expect((await request(app).get("/api/v1/candles/AUDUSD/H1")).status).toBe(400);
    expect((await request(app).get("/api/v1/candles/EURUSD/M15")).status).toBe(400);
  });
});
