import { randomUUID } from "crypto";
import type { NextFunction, Request, Response } from "express";

const requestIdPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;

export function requestContext(
  request: Request,
  response: Response,
  next: NextFunction,
): void {
  const suppliedRequestId = request.get("x-request-id");
  const requestId =
    suppliedRequestId && requestIdPattern.test(suppliedRequestId)
      ? suppliedRequestId
      : randomUUID();

  response.locals.requestId = requestId;
  response.setHeader("X-Request-Id", requestId);

  const sendJson = response.json.bind(response);
  response.json = ((body: unknown) => {
    if (
      body &&
      typeof body === "object" &&
      "error" in body &&
      body.error &&
      typeof body.error === "object" &&
      !("requestId" in body.error)
    ) {
      return sendJson({
        ...body,
        error: { ...body.error, requestId },
      });
    }
    return sendJson(body);
  }) as Response["json"];

  next();
}
