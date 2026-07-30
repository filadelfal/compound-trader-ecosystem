import { rateLimit } from "express-rate-limit";

function createLimiter(windowMs: number, limit: number) {
  return rateLimit({
    windowMs,
    limit,
    standardHeaders: "draft-8",
    legacyHeaders: false,
    skip: (request) => process.env.NODE_ENV === "test" && request.get("x-test-rate-limit") !== "enabled",
    handler: (_request, response) => {
      response.status(429).json({
        error: {
          code: "rate_limit_exceeded",
          requestId: response.locals.requestId,
        },
      });
    },
  });
}

export const authenticationRateLimit = createLimiter(15 * 60 * 1000, 100);
export const loginRateLimit = createLimiter(15 * 60 * 1000, 10);
export const recoveryRateLimit = createLimiter(60 * 60 * 1000, 5);
