import type { NextFunction, Request, Response } from "express";
import { logger } from "../logger";

export function notFoundHandler(
  _request: Request,
  response: Response,
): void {
  response.status(404).json({
    error: {
      code: "not_found",
      requestId: response.locals.requestId,
    },
  });
}

export function errorHandler(
  error: unknown,
  request: Request,
  response: Response,
  _next: NextFunction,
): void {
  logger.error({
    event: "request_failed",
    requestId: response.locals.requestId,
    method: request.method,
    path: request.path,
    error: error instanceof Error ? error.message : String(error),
  });

  if (response.headersSent) {
    return;
  }

  response.status(500).json({
    error: {
      code: "internal_error",
      requestId: response.locals.requestId,
    },
  });
}
