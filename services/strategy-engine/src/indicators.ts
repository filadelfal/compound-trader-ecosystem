export function exponentialMovingAverage(values: readonly number[], period: number): number[] {
  if (!Number.isInteger(period) || period < 2) {
    throw new Error("EMA period must be an integer greater than one");
  }
  if (values.length < period || values.some((value) => !Number.isFinite(value) || value <= 0)) {
    return [];
  }

  const multiplier = 2 / (period + 1);
  const seed = values.slice(0, period).reduce((sum, value) => sum + value, 0) / period;
  const output = [seed];

  for (const value of values.slice(period)) {
    output.push((value - output[output.length - 1]) * multiplier + output[output.length - 1]);
  }

  return output;
}

export interface OhlcCandle {
  high: number;
  low: number;
  close: number;
}

export interface AdxValue {
  adx: number;
  plusDI: number;
  minusDI: number;
}

export function averageDirectionalIndex(
  candles: readonly OhlcCandle[],
  period = 14
): AdxValue | null {
  if (!Number.isInteger(period) || period < 2) {
    throw new Error("ADX period must be an integer greater than one");
  }
  if (candles.length < period * 2 + 1 || candles.some((candle) =>
    !Number.isFinite(candle.high) || !Number.isFinite(candle.low) || !Number.isFinite(candle.close) ||
    candle.high <= 0 || candle.low <= 0 || candle.close <= 0 ||
    candle.high < candle.low || candle.close > candle.high || candle.close < candle.low
  )) {
    return null;
  }

  const trueRanges: number[] = [];
  const plusMovements: number[] = [];
  const minusMovements: number[] = [];

  for (let index = 1; index < candles.length; index += 1) {
    const current = candles[index];
    const previous = candles[index - 1];
    const upwardMove = current.high - previous.high;
    const downwardMove = previous.low - current.low;
    trueRanges.push(Math.max(
      current.high - current.low,
      Math.abs(current.high - previous.close),
      Math.abs(current.low - previous.close)
    ));
    plusMovements.push(upwardMove > downwardMove && upwardMove > 0 ? upwardMove : 0);
    minusMovements.push(downwardMove > upwardMove && downwardMove > 0 ? downwardMove : 0);
  }

  let smoothedRange = trueRanges.slice(0, period).reduce((sum, value) => sum + value, 0);
  let smoothedPlus = plusMovements.slice(0, period).reduce((sum, value) => sum + value, 0);
  let smoothedMinus = minusMovements.slice(0, period).reduce((sum, value) => sum + value, 0);
  const directionalValues: Array<{ plusDI: number; minusDI: number; dx: number }> = [];

  const appendDirectionalValue = () => {
    if (smoothedRange === 0) {
      directionalValues.push({ plusDI: 0, minusDI: 0, dx: 0 });
      return;
    }
    const plusDI = 100 * smoothedPlus / smoothedRange;
    const minusDI = 100 * smoothedMinus / smoothedRange;
    const total = plusDI + minusDI;
    directionalValues.push({
      plusDI,
      minusDI,
      dx: total === 0 ? 0 : 100 * Math.abs(plusDI - minusDI) / total
    });
  };

  appendDirectionalValue();
  for (let index = period; index < trueRanges.length; index += 1) {
    smoothedRange = smoothedRange - smoothedRange / period + trueRanges[index];
    smoothedPlus = smoothedPlus - smoothedPlus / period + plusMovements[index];
    smoothedMinus = smoothedMinus - smoothedMinus / period + minusMovements[index];
    appendDirectionalValue();
  }

  let adx = directionalValues.slice(0, period).reduce((sum, value) => sum + value.dx, 0) / period;
  for (const value of directionalValues.slice(period)) {
    adx = ((adx * (period - 1)) + value.dx) / period;
  }
  const latest = directionalValues.at(-1)!;
  return { adx, plusDI: latest.plusDI, minusDI: latest.minusDI };
}
