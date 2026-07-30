import request from "supertest";
import { app } from "../src/app";

describe("HTTP API hardening", () => {
  it("sets security headers and generates a request ID", async () => {
    const response = await request(app).get("/health");

    expect(response.status).toBe(200);
    expect(response.headers["x-powered-by"]).toBeUndefined();
    expect(response.headers["x-content-type-options"]).toBe("nosniff");
    expect(response.headers["x-request-id"]).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("retains a valid caller-provided request ID", async () => {
    const response = await request(app)
      .get("/health")
      .set("X-Request-Id", "gateway-request_123");

    expect(response.headers["x-request-id"]).toBe("gateway-request_123");
  });

  it("replaces an invalid caller-provided request ID", async () => {
    const response = await request(app)
      .get("/health")
      .set("X-Request-Id", "invalid request id");

    expect(response.headers["x-request-id"]).not.toBe("invalid request id");
  });

  it("returns request IDs in not-found errors", async () => {
    const response = await request(app).get("/does-not-exist");

    expect(response.status).toBe(404);
    expect(response.body.error.code).toBe("not_found");
    expect(response.body.error.requestId).toBe(response.headers["x-request-id"]);
  });

  it("adds request IDs to route-level validation errors", async () => {
    const response = await request(app)
      .post("/api/v1/auth/login")
      .send({});

    expect(response.status).toBe(400);
    expect(response.body.error.code).toBe("validation_error");
    expect(response.body.error.requestId).toBe(response.headers["x-request-id"]);
  });

  it("publishes an OpenAPI 3.1 document for every required API route", async () => {
    const response = await request(app).get("/openapi.json");

    expect(response.status).toBe(200);
    expect(response.body.openapi).toBe("3.1.0");
    expect(Object.keys(response.body.paths)).toEqual(
      expect.arrayContaining([
        "/auth/register",
        "/auth/verify-email",
        "/auth/resend-verification",
        "/auth/login",
        "/auth/refresh",
        "/auth/logout",
        "/auth/logout-all",
        "/auth/forgot-password",
        "/auth/reset-password",
        "/users/me",
      ]),
    );
  });

  it("rate limits repeated login attempts", async () => {
    let response: request.Response | undefined;

    for (let attempt = 0; attempt < 11; attempt += 1) {
      response = await request(app)
        .post("/api/v1/auth/login")
        .set("X-Test-Rate-Limit", "enabled")
        .send({});
    }

    expect(response?.status).toBe(429);
    expect(response?.body.error.code).toBe("rate_limit_exceeded");
    expect(response?.body.error.requestId).toBe(response?.headers["x-request-id"]);
  });
});
