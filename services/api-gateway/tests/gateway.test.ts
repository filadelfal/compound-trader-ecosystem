import jwt from "jsonwebtoken";
import request from "supertest";
import { createApp } from "../src/app";

const secret = "test_access_secret_0123456789_abcdefghijklmnopqrstuvwxyz_ABCDEFGH";
const userId = "11111111-1111-4111-8111-111111111111";

function token(overrides: Record<string, unknown> = {}) {
  return jwt.sign(
    { token_use: "access", session_id: "session-1", roles: ["trader"], ...overrides },
    secret,
    { algorithm: "HS256", issuer: "compound-trader", audience: "compound-trader-platform", subject: userId, expiresIn: "15m" },
  );
}

function testApp(fetchImpl: typeof fetch = jest.fn(async () => new Response(JSON.stringify({ ok: true }), {
  status: 200,
  headers: { "content-type": "application/json" },
})) as typeof fetch) {
  return createApp({
    auth: { secret, issuer: "compound-trader", audience: "compound-trader-platform" },
    userManagementUrl: "http://users:3002",
    tradingEngineUrl: "http://trading:3003",
    marketDataUrl: "http://market:3004",
    timeoutMs: 100,
    fetch: fetchImpl,
    readiness: async () => undefined,
  });
}

describe("API gateway", () => {
  it("serves the customer trading desk", async () => {
    const response = await request(testApp()).get("/");
    expect(response.status).toBe(200);
    expect(response.type).toContain("html");
    expect(response.text).toContain("Compound Trader");
    expect(response.text).toContain("Trade ticket");
  });

  it("reports health and readiness without authentication", async () => {
    expect((await request(testApp()).get("/health")).status).toBe(200);
    expect((await request(testApp()).get("/ready")).body.ready).toBe(true);
  });

  it("requires a valid access token for product APIs", async () => {
    const missing = await request(testApp()).get("/api/v1/portfolio");
    expect(missing.status).toBe(401);
    expect(missing.body.error.code).toBe("authentication_required");

    const refresh = token({ token_use: "refresh" });
    const invalid = await request(testApp()).get("/api/v1/portfolio").set("Authorization", `Bearer ${refresh}`);
    expect(invalid.status).toBe(401);
    expect(invalid.body.error.code).toBe("invalid_token");
  });

  it("routes public authentication and forwards tokens to protected profile APIs", async () => {
    const upstream = jest.fn(async () => new Response("{}", { status: 200, headers: { "content-type": "application/json" } })) as unknown as jest.MockedFunction<typeof fetch>;
    await request(testApp(upstream)).post("/api/v1/auth/login").send({ email: "trader@example.com", password: "password" });
    await request(testApp(upstream)).get("/api/v1/users/me").set("Authorization", `Bearer ${token()}`);
    expect(upstream.mock.calls[0][0]).toBe("http://users:3002/api/v1/auth/login");
    expect(new Headers(upstream.mock.calls[0][1]?.headers).has("authorization")).toBe(false);
    expect(upstream.mock.calls[1][0]).toBe("http://users:3002/api/v1/users/me");
    expect(new Headers(upstream.mock.calls[1][1]?.headers).get("authorization")).toBe(`Bearer ${token()}`);
  });

  it("proxies orders with the signed subject and never trusts a client user header", async () => {
    const upstream = jest.fn(async () => new Response(JSON.stringify({ id: "order-1" }), {
      status: 201,
      headers: { "content-type": "application/json", location: "/api/v1/orders/order-1" },
    })) as unknown as jest.MockedFunction<typeof fetch>;
    const response = await request(testApp(upstream))
      .post("/api/v1/orders")
      .set("Authorization", `Bearer ${token()}`)
      .set("X-User-ID", "attacker")
      .set("Idempotency-Key", "client-1")
      .send({ symbol: "AAPL", side: "buy", type: "limit", quantity: "1", limitPrice: "100" });

    expect(response.status).toBe(201);
    expect(response.headers.location).toBe("/api/v1/orders/order-1");
    const [url, init] = upstream.mock.calls[0] as [string | URL | Request, RequestInit | undefined];
    expect(url).toBe("http://trading:3003/api/v1/orders");
    expect(new Headers(init?.headers).get("x-user-id")).toBe(userId);
    expect(new Headers(init?.headers).get("idempotency-key")).toBe("client-1");
  });

  it("routes portfolio and quote requests to their respective services", async () => {
    const upstream = jest.fn(async () => new Response("{}", { status: 200, headers: { "content-type": "application/json" } })) as unknown as jest.MockedFunction<typeof fetch>;
    await request(testApp(upstream)).get("/api/v1/portfolio/valuation").set("Authorization", `Bearer ${token()}`);
    await request(testApp(upstream)).get("/api/v1/quotes?symbols=AAPL,MSFT").set("Authorization", `Bearer ${token()}`);
    expect(upstream.mock.calls[0][0]).toBe("http://trading:3003/api/v1/portfolio/valuation");
    expect(upstream.mock.calls[1][0]).toBe("http://market:3004/api/v1/quotes?symbols=AAPL,MSFT");
  });

  it("routes authenticated paper-account activation to trading", async () => {
    const upstream = jest.fn(async () => new Response("{}", { status: 201, headers: { "content-type": "application/json" } })) as unknown as jest.MockedFunction<typeof fetch>;
    const response = await request(testApp(upstream)).post("/api/v1/paper-account").set("Authorization", `Bearer ${token()}`).send({});
    expect(response.status).toBe(201);
    expect(upstream.mock.calls[0][0]).toBe("http://trading:3003/api/v1/paper-account");
    expect(new Headers(upstream.mock.calls[0][1]?.headers).get("x-user-id")).toBe(userId);
  });

  it("routes authenticated account-risk controls to trading", async () => {
    const upstream = jest.fn(async () => new Response("{}", { status: 200, headers: { "content-type": "application/json" } })) as unknown as jest.MockedFunction<typeof fetch>;
    const response = await request(testApp(upstream)).patch("/api/v1/risk").set("Authorization", `Bearer ${token()}`).send({ tradingEnabled: false });
    expect(response.status).toBe(200);
    expect(upstream.mock.calls[0][0]).toBe("http://trading:3003/api/v1/risk");
    expect(new Headers(upstream.mock.calls[0][1]?.headers).get("x-user-id")).toBe(userId);
  });

  it("preserves upstream failures and maps network errors", async () => {
    const rejected = jest.fn(async () => new Response(JSON.stringify({ error: { code: "invalid_order" } }), {
      status: 400,
      headers: { "content-type": "application/json" },
    })) as unknown as typeof fetch;
    expect((await request(testApp(rejected)).post("/api/v1/orders").set("Authorization", `Bearer ${token()}`).send({})).status).toBe(400);

    const failed = jest.fn(async () => { throw new Error("connection refused"); }) as unknown as typeof fetch;
    const response = await request(testApp(failed)).get("/api/v1/portfolio").set("Authorization", `Bearer ${token()}`);
    expect(response.status).toBe(502);
    expect(response.body.error.code).toBe("upstream_unavailable");
  });
});
