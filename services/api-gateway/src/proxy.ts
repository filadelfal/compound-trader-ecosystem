import { NextFunction, Response } from "express";
import { AuthenticatedRequest } from "./auth";

export type UpstreamFetch = typeof fetch;

export interface ProxyOptions {
  injectUserId?: boolean;
  forwardAuthorization?: boolean;
}

export function proxyTo(baseUrl: string, timeoutMs: number, upstreamFetch: UpstreamFetch = fetch, options: ProxyOptions = { injectUserId: true }) {
  return async (request: AuthenticatedRequest, response: Response, next: NextFunction): Promise<void> => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    try {
      const headers = new Headers({ accept: "application/json" });
      if (options.injectUserId !== false && request.auth) headers.set("x-user-id", request.auth.userId);
      if (options.forwardAuthorization) {
        const authorization = request.get("authorization");
        if (authorization) headers.set("authorization", authorization);
      }
      const contentType = request.get("content-type");
      const idempotencyKey = request.get("idempotency-key");
      if (contentType) headers.set("content-type", contentType);
      if (idempotencyKey) headers.set("idempotency-key", idempotencyKey);

      const method = request.method.toUpperCase();
      const upstream = await upstreamFetch(`${baseUrl}${request.originalUrl}`, {
        method,
        headers,
        body: method === "GET" || method === "HEAD" ? undefined : JSON.stringify(request.body ?? {}),
        signal: controller.signal,
      });
      response.status(upstream.status);
      for (const name of ["content-type", "location", "retry-after", "idempotent-replayed"]) {
        const value = upstream.headers.get(name);
        if (value) response.setHeader(name, value);
      }
      response.send(Buffer.from(await upstream.arrayBuffer()));
    } catch (error) {
      if (error instanceof Error && error.name === "AbortError") {
        response.status(504).json({ error: { code: "upstream_timeout", message: "The service did not respond in time" } });
        return;
      }
      next(error);
    } finally {
      clearTimeout(timer);
    }
  };
}
