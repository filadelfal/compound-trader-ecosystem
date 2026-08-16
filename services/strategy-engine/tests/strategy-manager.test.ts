import request from "supertest";
import { app } from "../src/app";
import { selectStrategySetup } from "../src/strategy-manager";

const requestId = "f9e28bb0-2b42-47c8-bd35-53a601ba02a1";
const accepted = (strategy: "EMA_PULLBACK" | "EMA_CROSSOVER" | "TREND_CONTINUATION" | "LONDON_BREAKOUT", direction: "BUY" | "SELL") => ({
  strategy,
  conditionsMet: true,
  direction,
  reasons: []
});
const rejected = (strategy: "EMA_PULLBACK" | "EMA_CROSSOVER" | "TREND_CONTINUATION" | "LONDON_BREAKOUT") => ({
  strategy,
  conditionsMet: false,
  direction: null,
  reasons: ["NO_SETUP"]
});

describe("strategy manager", () => {
  it("selects at most one setup and records same-direction confluence", () => {
    expect(selectStrategySetup({
      requestId,
      pair: "EURUSD",
      signals: [accepted("EMA_PULLBACK", "BUY"), accepted("TREND_CONTINUATION", "BUY")]
    })).toEqual({
      outcome: "SELECTED_SETUP",
      requestId,
      pair: "EURUSD",
      direction: "BUY",
      selectedStrategy: "TREND_CONTINUATION",
      confirmingStrategies: ["EMA_PULLBACK"],
      evaluations: 2
    });
  });

  it("fails closed when strategies disagree", () => {
    expect(selectStrategySetup({
      requestId,
      pair: "EURUSD",
      signals: [accepted("EMA_PULLBACK", "BUY"), accepted("EMA_CROSSOVER", "SELL")]
    })).toMatchObject({ outcome: "NO_TRADE", reason: "STRATEGY_CONFLICT" });
  });

  it("defaults to no trade when every strategy rejects", () => {
    expect(selectStrategySetup({
      requestId,
      pair: "EURUSD",
      signals: [rejected("EMA_PULLBACK"), rejected("EMA_CROSSOVER")]
    })).toMatchObject({ outcome: "NO_TRADE", reason: "NO_STRATEGY_SIGNAL" });
  });

  it("rejects duplicate strategies and inconsistent contracts", () => {
    expect(selectStrategySetup({
      requestId,
      pair: "EURUSD",
      signals: [accepted("EMA_PULLBACK", "BUY"), accepted("EMA_PULLBACK", "BUY")]
    })).toMatchObject({ outcome: "NO_TRADE", reason: "DUPLICATE_STRATEGY" });
    expect(selectStrategySetup({
      requestId,
      pair: "EURUSD",
      signals: [{ ...accepted("EMA_PULLBACK", "BUY"), reasons: ["contradiction"] }]
    })).toMatchObject({ outcome: "NO_TRADE", reason: "INVALID_INPUT" });
  });

  it("exposes a manager endpoint that defaults to no trade", async () => {
    const response = await request(app).post("/api/v1/strategies/select").send({});
    expect(response.status).toBe(200);
    expect(response.body).toMatchObject({ outcome: "NO_TRADE", reason: "INVALID_INPUT" });
  });
});
