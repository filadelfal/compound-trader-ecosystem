import type { NextFunction, Request, RequestHandler, Response } from "express";

export interface RateLimitResult {
  count: number;
  ttlSeconds: number;
}

export interface RateLimitStore {
  increment(key: string, windowSeconds: number): Promise<RateLimitResult>;
}

interface EvalClient {
  eval(
    script: string,
    options: { keys: string[]; arguments: string[] },
  ): Promise<unknown>;
}

const incrementScript = `
local current = redis.call('INCR', KEYS[1])
if current == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
local ttl = redis.call('TTL', KEYS[1])
return {current, ttl}
`;

export class RedisRateLimitStore implements RateLimitStore {
  public constructor(private readonly client: EvalClient) {}

  public async increment(key: string, windowSeconds: number): Promise<RateLimitResult> {
    const result = await this.client.eval(incrementScript, {
      keys: [key],
      arguments: [String(windowSeconds)],
    });
    if (!Array.isArray(result) || result.length !== 2) throw new Error("Invalid rate-limit response");
    return { count: Number(result[0]), ttlSeconds: Math.max(0, Number(result[1])) };
  }
}

function clientKey(request: Request): string {
  return request.ip || request.socket.remoteAddress || "unknown";
}

export function createRateLimit(
  store: RateLimitStore,
  options: { prefix: string; max: number; windowSeconds: number },
): RequestHandler {
  return async (request: Request, response: Response, next: NextFunction) => {
    try {
      const result = await store.increment(`${options.prefix}:${clientKey(request)}`, options.windowSeconds);
      const remaining = Math.max(0, options.max - result.count);
      response.setHeader("RateLimit-Limit", String(options.max));
      response.setHeader("RateLimit-Remaining", String(remaining));
      response.setHeader("RateLimit-Reset", String(result.ttlSeconds));
      if (result.count > options.max) {
        response.setHeader("Retry-After", String(result.ttlSeconds));
        response.status(429).json({ error: { code: "rate_limit_exceeded" } });
        return;
      }
      next();
    } catch {
      response.status(503).json({ error: { code: "rate_limit_unavailable" } });
    }
  };
}
