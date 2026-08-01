import { randomUUID } from "node:crypto";

import type { NextFunction, Request, Response } from "express";
import client from "prom-client";

import { logger } from "./logger";

const requestIdPattern = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;

const requests = new client.Counter({
  name: "api_gateway_http_requests_total",
  help: "HTTP requests handled by the API gateway",
  labelNames: ["method", "route_group", "status_class"] as const,
});

const duration = new client.Histogram({
  name: "api_gateway_http_request_duration_seconds",
  help: "API gateway request duration in seconds",
  labelNames: ["method", "route_group"] as const,
  buckets: [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5],
});

declare global {
  namespace Express {
    interface Request {
      requestId?: string;
    }
  }
}

function routeGroup(path: string): string {
  if (path.startsWith("/api/v1/auth")) return "auth";
  if (path.startsWith("/api/v1/users")) return "users";
  if (path === "/health" || path === "/ready") return "health";
  if (path === "/metrics") return "metrics";
  if (path.startsWith("/api/")) return "api_other";
  return "other";
}

export function observeRequests(request: Request, response: Response, next: NextFunction): void {
  const supplied = request.get("x-request-id");
  const requestId = supplied && requestIdPattern.test(supplied) ? supplied : randomUUID();
  const group = routeGroup(request.path);
  const started = process.hrtime.bigint();

  request.requestId = requestId;
  response.setHeader("X-Request-Id", requestId);

  response.once("finish", () => {
    const elapsedSeconds = Number(process.hrtime.bigint() - started) / 1_000_000_000;
    const statusClass = `${Math.floor(response.statusCode / 100)}xx`;
    requests.inc({ method: request.method, route_group: group, status_class: statusClass });
    duration.observe({ method: request.method, route_group: group }, elapsedSeconds);
    logger.info({
      event: "http_request_completed",
      request_id: requestId,
      method: request.method,
      route_group: group,
      status_code: response.statusCode,
      duration_ms: Math.round(elapsedSeconds * 1000),
    });
  });
  next();
}
