import request from "supertest";
import { app } from "../src/app";

describe("health endpoint", () => {
  it("returns service health", async () => {
    const response = await request(app).get("/health");
    expect(response.status).toBe(200);
    expect(response.body.status).toBe("ok");
    expect(response.headers["x-request-id"]).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("preserves a valid caller request id", async () => {
    const response = await request(app).get("/health").set("X-Request-Id", "client-request_123");
    expect(response.headers["x-request-id"]).toBe("client-request_123");
  });

  it("replaces malformed request ids", async () => {
    const response = await request(app).get("/health").set("X-Request-Id", "bad id with spaces");
    expect(response.headers["x-request-id"]).not.toBe("bad id with spaces");
  });
});
