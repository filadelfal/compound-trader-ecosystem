import type { NextFunction, Request, RequestHandler, Response } from "express";

import { config } from "./config";

type Fetch = typeof fetch;

const RESPONSE_HEADERS = ["content-type", "content-language", "etag", "last-modified"];

function requestBody(request: Request): string | undefined {
  if (request.method === "GET" || request.method === "HEAD" || request.body === undefined) {
    return undefined;
  }
  return JSON.stringify(request.body);
}

function upstreamHeaders(request: Request): Headers {
  const headers = new Headers();
  headers.set("accept", request.get("accept") ?? "application/json");
  headers.set("content-type", "application/json");

  const authorization = request.get("authorization");
  if (authorization) headers.set("authorization", authorization);

  const requestId = request.get("x-request-id");
  if (requestId) headers.set("x-request-id", requestId);

  if (request.auth) {
    headers.set("x-authenticated-user-id", request.auth.userId);
    headers.set("x-authenticated-session-id", request.auth.sessionId);
    headers.set("x-authenticated-roles", request.auth.roles.join(","));
  }
  return headers;
}

export function createServiceProxy(
  upstream: string,
  fetchImplementation?: Fetch,
): RequestHandler {
  const baseUrl = upstream.replace(/\/$/, "");

  return async (request: Request, response: Response, next: NextFunction) => {
    try {
      const executeFetch = fetchImplementation ?? globalThis.fetch;
      const upstreamResponse = await executeFetch(`${baseUrl}${request.originalUrl}`, {
        method: request.method,
        headers: upstreamHeaders(request),
        body: requestBody(request),
        redirect: "manual",
        signal: AbortSignal.timeout(config.UPSTREAM_TIMEOUT_MS),
      });

      const body = Buffer.from(await upstreamResponse.arrayBuffer());
      if (body.byteLength > config.UPSTREAM_MAX_RESPONSE_BYTES) {
        response.status(502).json({ error: { code: "upstream_response_too_large" } });
        return;
      }

      for (const header of RESPONSE_HEADERS) {
        const value = upstreamResponse.headers.get(header);
        if (value) response.setHeader(header, value);
      }
      response.status(upstreamResponse.status).send(body);
    } catch (error) {
      if (error instanceof Error && (error.name === "TimeoutError" || error.name === "AbortError")) {
        response.status(504).json({ error: { code: "upstream_timeout" } });
        return;
      }
      next(error);
    }
  };
}
