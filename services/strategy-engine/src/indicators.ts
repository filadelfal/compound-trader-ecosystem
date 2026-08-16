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
