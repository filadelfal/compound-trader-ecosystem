import jwt from "jsonwebtoken";
import request from "supertest";

import { app } from "../src/app";
import { config } from "../src/config";

function token(): string {
  return jwt.sign(
    { token_use: "access", session_id: "session-9", roles: ["user"] },
    config.JWT_ACCESS_SECRET,
    { algorithm: "HS256", subject: "user-9", issuer: config.JWT_ISSUER, audience: config.JWT_AUDIENCE },
  );
}

describe("allowlisted service routing", () => {
  const originalFetch = global.fetch;

  afterEach(() => {
    global.fetch = originalFetch;
    jest.restoreAllMocks();
  });

  it("forwards public authentication requests and their JSON body", async () => {
    const fetchMock = jest.fn(async () => new Response(JSON.stringify({ data: { accepted: true } }), {
      status: 202,
      headers: { "content-type": "application/json", "set-cookie": "must-not-forward=true" },
    }));
    global.fetch = fetchMock as typeof fetch;

    const response = await request(app).post("/api/v1/auth/login?source=web").send({ email: "person@example.com" });
    expect(response.status).toBe(202);
    expect(response.headers["set-cookie"]).toBeUndefined();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    const [url, options] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe(`${config.USER_MANAGEMENT_URL}/api/v1/auth/login?source=web`);
    expect(options.body).toBe(JSON.stringify({ email: "person@example.com" }));
  });

  it("requires authentication and overwrites trusted identity headers", async () => {
    const fetchMock = jest.fn(async (_url: string | URL | globalThis.Request, options?: RequestInit) => {
      const headers = new Headers(options?.headers);
      return new Response(JSON.stringify({
        userId: headers.get("x-authenticated-user-id"),
        roles: headers.get("x-authenticated-roles"),
      }), { status: 200, headers: { "content-type": "application/json" } });
    });
    global.fetch = fetchMock as typeof fetch;

    expect((await request(app).get("/api/v1/users/me")).status).toBe(401);
    const response = await request(app)
      .get("/api/v1/users/me")
      .set("Authorization", `Bearer ${token()}`)
      .set("X-Authenticated-User-Id", "attacker-controlled");

    expect(response.status).toBe(200);
    expect(response.body).toEqual({ userId: "user-9", roles: "user" });
  });

  it("does not proxy unknown service paths", async () => {
    const fetchMock = jest.fn();
    global.fetch = fetchMock as typeof fetch;
    expect((await request(app).get("/api/v1/unlisted/resource")).status).toBe(404);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("returns a stable gateway error when the upstream is unavailable", async () => {
    global.fetch = jest.fn(async () => {
      throw new Error("connection refused: internal detail");
    }) as typeof fetch;

    const response = await request(app).post("/api/v1/auth/login").send({});
    expect(response.status).toBe(502);
    expect(response.body).toEqual({
      error: { code: "upstream_unavailable", message: "The requested service is unavailable" },
    });
    expect(JSON.stringify(response.body)).not.toContain("connection refused");
  });

  it("maps upstream timeouts to 504", async () => {
    global.fetch = jest.fn(async () => {
      const error = new Error("timed out");
      error.name = "TimeoutError";
      throw error;
    }) as typeof fetch;

    const response = await request(app).post("/api/v1/auth/login").send({});
    expect(response.status).toBe(504);
    expect(response.body.error.code).toBe("upstream_timeout");
  });
});
