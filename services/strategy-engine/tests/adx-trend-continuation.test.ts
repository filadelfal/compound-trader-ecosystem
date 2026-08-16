import request from "supertest";
import { app } from "../src/app";
import { evaluateAdxTrendContinuation } from "../src/adx-trend-continuation";
import { averageDirectionalIndex } from "../src/indicators";

function trendingCandles(count: number, hours: number, step = 0.001) {
  return Array.from({ length: count }, (_, index) => {
    const open = 1.1 + index * step;
    const close = open + step * 0.8;
    return {
      open,
      high: Math.max(open, close) + Math.abs(step) * 0.1,
      low: Math.min(open, close) - Math.abs(step) * 0.1,
      close,
      closeTime: new Date(Date.UTC(2026, 7, 1, index * hours)).toISOString(),
      status: "CLOSED" as const
    };
  });
}

describe("Wilder ADX", () => {
  it("reports strong positive directional movement for a persistent uptrend", () => {
    const value = averageDirectionalIndex(trendingCandles(40, 1));
    expect(value).not.toBeNull();
    expect(value!.adx).toBeGreaterThanOrEqual(25);
    expect(value!.plusDI).toBeGreaterThan(value!.minusDI);
  });

  it("returns no value for insufficient or inconsistent candles", () => {
    expect(averageDirectionalIndex(trendingCandles(10, 1))).toBeNull();
    expect(averageDirectionalIndex([{ high: 1, low: 2, close: 1.5 }], 2)).toBeNull();
  });
});

describe("ADX trend continuation strategy", () => {
  it("confirms a strong aligned trend only after an H1 breakout", () => {
    const result = evaluateAdxTrendContinuation({
      pair: "EURUSD",
      h1: trendingCandles(40, 1),
      h4: trendingCandles(40, 4)
    });
    expect(result).toMatchObject({
      strategy: "TREND_CONTINUATION",
      conditionsMet: true,
      direction: "BUY",
      reasons: []
    });
  });

  it("fails closed without sufficient candle history", () => {
    expect(evaluateAdxTrendContinuation({
      pair: "EURUSD",
      h1: trendingCandles(10, 1),
      h4: trendingCandles(10, 4)
    })).toMatchObject({ conditionsMet: false, reasons: ["INVALID_INPUT"] });
  });

  it("rejects unordered candles", () => {
    const h1 = trendingCandles(40, 1);
    h1[39].closeTime = h1[38].closeTime;
    expect(evaluateAdxTrendContinuation({ pair: "EURUSD", h1, h4: trendingCandles(40, 4) }))
      .toMatchObject({ conditionsMet: false, reasons: ["NON_CHRONOLOGICAL_CANDLES"] });
  });

  it("exposes a signal-only endpoint that defaults to no signal", async () => {
    const response = await request(app).post("/api/v1/strategies/trend-continuation/evaluate").send({});
    expect(response.status).toBe(200);
    expect(response.body).toMatchObject({
      strategy: "TREND_CONTINUATION",
      conditionsMet: false,
      direction: null,
      reasons: ["INVALID_INPUT"]
    });
  });
});
