import jwt from "jsonwebtoken";
import request from "supertest";

import { app } from "../src/app";
import { config } from "../src/config";

function accessToken(roles: string[] = ["user"]): string {
  return jwt.sign(
    { token_use: "access", session_id: "session-1", roles },
    config.JWT_ACCESS_SECRET,
    {
      algorithm: "HS256",
      subject: "user-1",
      issuer: config.JWT_ISSUER,
      audience: config.JWT_AUDIENCE,
      expiresIn: "5m",
    },
  );
}

describe("gateway authentication boundary", () => {
  it("rejects missing credentials", async () => {
    const response = await request(app).get("/api/v1/me");
    expect(response.status).toBe(401);
    expect(response.body.error.code).toBe("authentication_required");
  });

  it("accepts a valid access token and exposes trusted identity", async () => {
    const response = await request(app)
      .get("/api/v1/me")
      .set("Authorization", `Bearer ${accessToken()}`);
    expect(response.status).toBe(200);
    expect(response.body.data).toEqual({ userId: "user-1", sessionId: "session-1", roles: ["user"] });
  });

  it("rejects refresh tokens and invalid signatures", async () => {
    const refresh = jwt.sign(
      { token_use: "refresh", session_id: "session-1", roles: [] },
      config.JWT_ACCESS_SECRET,
      { algorithm: "HS256", subject: "user-1", issuer: config.JWT_ISSUER, audience: config.JWT_AUDIENCE },
    );
    const response = await request(app).get("/api/v1/me").set("Authorization", `Bearer ${refresh}`);
    expect(response.status).toBe(401);
    expect(response.body.error.code).toBe("invalid_token");
  });

  it("enforces role authorization", async () => {
    const forbidden = await request(app)
      .get("/api/v1/admin/ping")
      .set("Authorization", `Bearer ${accessToken(["user"])}`);
    expect(forbidden.status).toBe(403);

    const allowed = await request(app)
      .get("/api/v1/admin/ping")
      .set("Authorization", `Bearer ${accessToken(["admin"])}`);
    expect(allowed.status).toBe(200);
  });
});
