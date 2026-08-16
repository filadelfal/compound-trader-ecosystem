import { z } from "zod";
import { exponentialMovingAverage } from "./indicators";
import { supportedPairs } from "./decision";

const closedCandleSchema = z.object({
  closeTime: z.string().datetime({ offset: true }),
  close: z.number().positive(),
  status: z.literal("CLOSED")
}).strict();

export const emaPullbackInputSchema = z.object({
  pair: z.enum(supportedPairs),
  h1: z.array(closedCandleSchema).min(22),
  h4: z.array(closedCandleSchema).min(22)
}).strict();

export type EmaPullbackReason =
  | "INVALID_INPUT"
  | "NON_CHRONOLOGICAL_CANDLES"
  | "NO_TIMEFRAME_TREND"
  | "TIMEFRAME_CONFLICT"
  | "NO_PULLBACK_CONFIRMATION";

export type EmaPullbackResult =
  | { strategy: "EMA_PULLBACK"; conditionsMet: true; direction: "BUY" | "SELL"; reasons: [] }
  | { strategy: "EMA_PULLBACK"; conditionsMet: false; direction: null; reasons: EmaPullbackReason[] };

function isChronological(candles: readonly { closeTime: string }[]): boolean {
  return candles.every((candle, index) =>
    index === 0 || Date.parse(candle.closeTime) > Date.parse(candles[index - 1].closeTime)
  );
}

function trend(fast: number, slow: number): "BUY" | "SELL" | null {
  if (fast > slow) return "BUY";
  if (fast < slow) return "SELL";
  return null;
}

export function evaluateEmaPullback(rawInput: unknown): EmaPullbackResult {
  const parsed = emaPullbackInputSchema.safeParse(rawInput);
  if (!parsed.success) {
    return { strategy: "EMA_PULLBACK", conditionsMet: false, direction: null, reasons: ["INVALID_INPUT"] };
  }

  const { h1, h4 } = parsed.data;
  if (!isChronological(h1) || !isChronological(h4)) {
    return {
      strategy: "EMA_PULLBACK",
      conditionsMet: false,
      direction: null,
      reasons: ["NON_CHRONOLOGICAL_CANDLES"]
    };
  }

  const h1Closes = h1.map((candle) => candle.close);
  const h4Closes = h4.map((candle) => candle.close);
  const h1Fast = exponentialMovingAverage(h1Closes, 8);
  const h1Slow = exponentialMovingAverage(h1Closes, 21);
  const h4Fast = exponentialMovingAverage(h4Closes, 8);
  const h4Slow = exponentialMovingAverage(h4Closes, 21);

  const h1Trend = trend(h1Fast.at(-1)!, h1Slow.at(-1)!);
  const h4Trend = trend(h4Fast.at(-1)!, h4Slow.at(-1)!);
  if (!h1Trend || !h4Trend) {
    return { strategy: "EMA_PULLBACK", conditionsMet: false, direction: null, reasons: ["NO_TIMEFRAME_TREND"] };
  }
  if (h1Trend !== h4Trend) {
    return { strategy: "EMA_PULLBACK", conditionsMet: false, direction: null, reasons: ["TIMEFRAME_CONFLICT"] };
  }

  const previousClose = h1Closes.at(-2)!;
  const latestClose = h1Closes.at(-1)!;
  const previousFast = h1Fast.at(-2)!;
  const previousSlow = h1Slow.at(-2)!;
  const latestFast = h1Fast.at(-1)!;
  const latestSlow = h1Slow.at(-1)!;

  const confirmed = h1Trend === "BUY"
    ? previousClose <= previousFast && previousClose >= previousSlow && latestClose > latestFast && latestFast > latestSlow
    : previousClose >= previousFast && previousClose <= previousSlow && latestClose < latestFast && latestFast < latestSlow;

  if (!confirmed) {
    return {
      strategy: "EMA_PULLBACK",
      conditionsMet: false,
      direction: null,
      reasons: ["NO_PULLBACK_CONFIRMATION"]
    };
  }

  return { strategy: "EMA_PULLBACK", conditionsMet: true, direction: h1Trend, reasons: [] };
}
