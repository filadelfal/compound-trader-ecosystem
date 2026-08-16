import { z } from "zod";
import { supportedPairs } from "./decision";
import { averageDirectionalIndex } from "./indicators";

const candleSchema = z.object({
  closeTime: z.string().datetime({ offset: true }),
  open: z.number().positive(),
  high: z.number().positive(),
  low: z.number().positive(),
  close: z.number().positive(),
  status: z.literal("CLOSED")
}).strict().refine((candle) =>
  candle.high >= Math.max(candle.open, candle.close) &&
  candle.low <= Math.min(candle.open, candle.close),
  "OHLC values are inconsistent"
);

export const adxTrendInputSchema = z.object({
  pair: z.enum(supportedPairs),
  h1: z.array(candleSchema).min(30),
  h4: z.array(candleSchema).min(30)
}).strict();

export type AdxTrendReason =
  | "INVALID_INPUT"
  | "NON_CHRONOLOGICAL_CANDLES"
  | "WEAK_TREND"
  | "TIMEFRAME_CONFLICT"
  | "NO_BREAKOUT_CONFIRMATION";

export type AdxTrendResult =
  | {
      strategy: "TREND_CONTINUATION";
      conditionsMet: true;
      direction: "BUY" | "SELL";
      reasons: [];
      strength: { h1Adx: number; h4Adx: number };
    }
  | {
      strategy: "TREND_CONTINUATION";
      conditionsMet: false;
      direction: null;
      reasons: AdxTrendReason[];
    };

function chronological(candles: readonly { closeTime: string }[]): boolean {
  return candles.every((candle, index) =>
    index === 0 || Date.parse(candle.closeTime) > Date.parse(candles[index - 1].closeTime)
  );
}

export function evaluateAdxTrendContinuation(rawInput: unknown): AdxTrendResult {
  const parsed = adxTrendInputSchema.safeParse(rawInput);
  if (!parsed.success) {
    return { strategy: "TREND_CONTINUATION", conditionsMet: false, direction: null, reasons: ["INVALID_INPUT"] };
  }

  const { h1, h4 } = parsed.data;
  if (!chronological(h1) || !chronological(h4)) {
    return {
      strategy: "TREND_CONTINUATION",
      conditionsMet: false,
      direction: null,
      reasons: ["NON_CHRONOLOGICAL_CANDLES"]
    };
  }

  const h1Adx = averageDirectionalIndex(h1)!;
  const h4Adx = averageDirectionalIndex(h4)!;
  if (h1Adx.adx < 20 || h4Adx.adx < 25) {
    return { strategy: "TREND_CONTINUATION", conditionsMet: false, direction: null, reasons: ["WEAK_TREND"] };
  }

  const h1Direction = h1Adx.plusDI > h1Adx.minusDI ? "BUY" : h1Adx.minusDI > h1Adx.plusDI ? "SELL" : null;
  const h4Direction = h4Adx.plusDI > h4Adx.minusDI ? "BUY" : h4Adx.minusDI > h4Adx.plusDI ? "SELL" : null;
  if (!h1Direction || h1Direction !== h4Direction) {
    return { strategy: "TREND_CONTINUATION", conditionsMet: false, direction: null, reasons: ["TIMEFRAME_CONFLICT"] };
  }

  const latest = h1.at(-1)!;
  const previous = h1.at(-2)!;
  const breakoutConfirmed = h1Direction === "BUY"
    ? latest.close > previous.high
    : latest.close < previous.low;
  if (!breakoutConfirmed) {
    return {
      strategy: "TREND_CONTINUATION",
      conditionsMet: false,
      direction: null,
      reasons: ["NO_BREAKOUT_CONFIRMATION"]
    };
  }

  return {
    strategy: "TREND_CONTINUATION",
    conditionsMet: true,
    direction: h1Direction,
    reasons: [],
    strength: { h1Adx: h1Adx.adx, h4Adx: h4Adx.adx }
  };
}
