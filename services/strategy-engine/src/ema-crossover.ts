import { z } from "zod";
import { supportedPairs } from "./decision";
import { exponentialMovingAverage } from "./indicators";

const candleSchema = z.object({
  closeTime: z.string().datetime({ offset: true }),
  close: z.number().positive(),
  status: z.literal("CLOSED")
}).strict();

export const emaCrossoverInputSchema = z.object({
  pair: z.enum(supportedPairs),
  h1: z.array(candleSchema).min(22),
  h4: z.array(candleSchema).min(22)
}).strict();

export type EmaCrossoverReason =
  | "INVALID_INPUT"
  | "NON_CHRONOLOGICAL_CANDLES"
  | "NO_FRESH_CROSSOVER"
  | "H4_TREND_CONFLICT"
  | "CROSSOVER_TOO_WEAK";

export type EmaCrossoverResult =
  | { strategy: "EMA_CROSSOVER"; conditionsMet: true; direction: "BUY" | "SELL"; reasons: []; separationPips: number }
  | { strategy: "EMA_CROSSOVER"; conditionsMet: false; direction: null; reasons: EmaCrossoverReason[] };

const MIN_SEPARATION_PIPS = 0.5;

function chronological(candles: readonly { closeTime: string }[]): boolean {
  return candles.every((candle, index) =>
    index === 0 || Date.parse(candle.closeTime) > Date.parse(candles[index - 1].closeTime)
  );
}

function pipSize(pair: typeof supportedPairs[number]): number {
  return pair === "USDJPY" ? 0.01 : 0.0001;
}

export function evaluateEmaCrossover(rawInput: unknown): EmaCrossoverResult {
  const parsed = emaCrossoverInputSchema.safeParse(rawInput);
  if (!parsed.success) {
    return { strategy: "EMA_CROSSOVER", conditionsMet: false, direction: null, reasons: ["INVALID_INPUT"] };
  }

  const { pair, h1, h4 } = parsed.data;
  if (!chronological(h1) || !chronological(h4)) {
    return {
      strategy: "EMA_CROSSOVER",
      conditionsMet: false,
      direction: null,
      reasons: ["NON_CHRONOLOGICAL_CANDLES"]
    };
  }

  const h1Closes = h1.map((candle) => candle.close);
  const h4Closes = h4.map((candle) => candle.close);
  const h1Fast = exponentialMovingAverage(h1Closes, 8);
  const h1Slow = exponentialMovingAverage(h1Closes, 21);
  const h4Fast = exponentialMovingAverage(h4Closes, 8).at(-1)!;
  const h4Slow = exponentialMovingAverage(h4Closes, 21).at(-1)!;
  const previousFast = h1Fast.at(-2)!;
  const previousSlow = h1Slow.at(-2)!;
  const latestFast = h1Fast.at(-1)!;
  const latestSlow = h1Slow.at(-1)!;

  const direction = previousFast <= previousSlow && latestFast > latestSlow
    ? "BUY"
    : previousFast >= previousSlow && latestFast < latestSlow
      ? "SELL"
      : null;

  if (!direction) {
    return { strategy: "EMA_CROSSOVER", conditionsMet: false, direction: null, reasons: ["NO_FRESH_CROSSOVER"] };
  }

  const h4Direction = h4Fast > h4Slow ? "BUY" : h4Fast < h4Slow ? "SELL" : null;
  if (h4Direction !== direction) {
    return { strategy: "EMA_CROSSOVER", conditionsMet: false, direction: null, reasons: ["H4_TREND_CONFLICT"] };
  }

  const separationPips = Math.abs(latestFast - latestSlow) / pipSize(pair);
  if (separationPips < MIN_SEPARATION_PIPS) {
    return { strategy: "EMA_CROSSOVER", conditionsMet: false, direction: null, reasons: ["CROSSOVER_TOO_WEAK"] };
  }

  return {
    strategy: "EMA_CROSSOVER",
    conditionsMet: true,
    direction,
    reasons: [],
    separationPips
  };
}
