import type { NextFunction, Request, Response } from "express";

import { createRateLimit, type RateLimitStore } from "../src/rate-limit";

function responseDouble() {
  const headers = new Map<string, string>();
  const response = {
    setHeader: jest.fn((name: string, value: string) => headers.set(name, value)),
    status: jest.fn().mockReturnThis(),
    json: jest.fn().mockReturnThis(),
  } as unknown as Response;
  return { response, headers };
}

describe("distributed rate limiting", () => {
  const request = { ip: "203.0.113.10", socket: {} } as Request;

  it("allows requests within the limit", async () => {
    const store: RateLimitStore = { increment: jest.fn().mockResolvedValue({ count: 2, ttlSeconds: 42 }) };
    const middleware = createRateLimit(store, { prefix: "auth", max: 3, windowSeconds: 60 });
    const { response, headers } = responseDouble();
    const next = jest.fn() as NextFunction;

    await middleware(request, response, next);
    expect(next).toHaveBeenCalledTimes(1);
    expect(headers.get("RateLimit-Remaining")).toBe("1");
  });

  it("blocks requests above the limit", async () => {
    const store: RateLimitStore = { increment: jest.fn().mockResolvedValue({ count: 4, ttlSeconds: 31 }) };
    const middleware = createRateLimit(store, { prefix: "auth", max: 3, windowSeconds: 60 });
    const { response, headers } = responseDouble();

    await middleware(request, response, jest.fn());
    expect(response.status).toHaveBeenCalledWith(429);
    expect(headers.get("Retry-After")).toBe("31");
  });

  it("fails closed when the shared store is unavailable", async () => {
    const store: RateLimitStore = { increment: jest.fn().mockRejectedValue(new Error("redis unavailable")) };
    const middleware = createRateLimit(store, { prefix: "auth", max: 3, windowSeconds: 60 });
    const { response } = responseDouble();

    await middleware(request, response, jest.fn());
    expect(response.status).toHaveBeenCalledWith(503);
    expect(response.json).toHaveBeenCalledWith({ error: { code: "rate_limit_unavailable" } });
  });
});
