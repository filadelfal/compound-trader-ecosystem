import request from "supertest";
import { app } from "../src/app";
import { evaluateLondonBreakout } from "../src/london-breakout";

function candle(hour: number, open: number, high: number, low: number, close: number) {
  return {
    openTime: new Date(Date.UTC(2026, 7, 3, hour)).toISOString(),
    closeTime: new Date(Date.UTC(2026, 7, 3, hour + 1)).toISOString(),
    open,
    high,
    low,
    close,
    status: "CLOSED" as const
  };
}

function validSession(breakoutClose = 1.1025) {
  const range = Array.from({ length: 8 }, (_, hour) =>
    candle(hour, 1.1008, 1.102, 1.1, 1.101)
  );
  return [...range, candle(8, 1.101, 1.103, 1.1008, breakoutClose)];
}

describe("London breakout strategy", () => {
  it("confirms a buffered breakout from a complete controlled range", () => {
    const result = evaluateLondonBreakout({ pair: "EURUSD", h1: validSession(), alreadySignaled: false });
    expect(result).toMatchObject({
      strategy: "LONDON_BREAKOUT",
      conditionsMet: true,
      direction: "BUY",
      reasons: [],
      range: { high: 1.102, low: 1.1 }
    });
  });

  it("rejects an unconfirmed break and a repeated session signal", () => {
    expect(evaluateLondonBreakout({ pair: "EURUSD", h1: validSession(1.1021), alreadySignaled: false }))
      .toMatchObject({ conditionsMet: false, reasons: ["NO_BREAKOUT_CONFIRMATION"] });
    expect(evaluateLondonBreakout({ pair: "EURUSD", h1: validSession(), alreadySignaled: true }))
      .toMatchObject({ conditionsMet: false, reasons: ["ALREADY_SIGNALED"] });
  });

  it("rejects unsupported pairs and incomplete ranges", () => {
    expect(evaluateLondonBreakout({ pair: "USDJPY", h1: validSession(), alreadySignaled: false }))
      .toMatchObject({ conditionsMet: false, reasons: ["INVALID_INPUT"] });
    expect(evaluateLondonBreakout({ pair: "EURUSD", h1: validSession().slice(1), alreadySignaled: false }))
      .toMatchObject({ conditionsMet: false, reasons: ["INVALID_INPUT"] });
  });

  it("exposes a signal-only endpoint that defaults to no signal", async () => {
    const response = await request(app).post("/api/v1/strategies/london-breakout/evaluate").send({});
    expect(response.status).toBe(200);
    expect(response.body).toMatchObject({
      strategy: "LONDON_BREAKOUT",
      conditionsMet: false,
      direction: null,
      reasons: ["INVALID_INPUT"]
    });
  });
});
