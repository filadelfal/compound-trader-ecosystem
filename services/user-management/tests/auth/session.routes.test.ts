import express from "express";
import request from "supertest";

import { JwtTokenError } from "../../src/auth/jwt";
import { createSessionRouter } from "../../src/auth/session/session.routes";

function testApp(overrides: Record<string, jest.Mock> = {}) {
  const sessions = {
    rotateRefreshToken: jest.fn().mockResolvedValue({
      accessToken: "next-access",
      refreshToken: "next-refresh",
    }),
    logout: jest.fn().mockResolvedValue(true),
    logoutAllDevices: jest.fn().mockResolvedValue(2),
    ...overrides,
  };
  const jwt = {
    verifyRefreshToken: jest.fn().mockReturnValue({
      subject: "user-1",
      session_id: "session-1",
    }),
    verifyAccessToken: jest.fn().mockReturnValue({
      subject: "user-1",
      session_id: "session-1",
      roles: ["user"],
    }),
  };
  const resolveRoles = jest.fn().mockResolvedValue(["user"]);
  const app = express();
  app.use(express.json());
  app.use("/api/v1/auth", createSessionRouter({
    sessions: sessions as never,
    jwt: jwt as never,
    resolveRoles,
  }));
  app.use((_error: unknown, _request: express.Request, response: express.Response, _next: express.NextFunction) => {
    response.status(500).json({ error: { code: "internal_error" } });
  });
  return { app, sessions, jwt, resolveRoles };
}

describe("session routes", () => {
  it("rotates refresh tokens with roles resolved by the server", async () => {
    const { app, sessions, resolveRoles } = testApp();
    const response = await request(app)
      .post("/api/v1/auth/refresh")
      .send({ refreshToken: "current-refresh" });

    expect(response.status).toBe(200);
    expect(resolveRoles).toHaveBeenCalledWith("user-1");
    expect(sessions.rotateRefreshToken).toHaveBeenCalledWith(
      "current-refresh",
      ["user"],
    );
    expect(response.body.data).toEqual({
      accessToken: "next-access",
      refreshToken: "next-refresh",
      tokenType: "Bearer",
    });
  });

  it("rejects invalid refresh tokens without exposing the cause", async () => {
    const { app, jwt } = testApp();
    jwt.verifyRefreshToken.mockImplementation(() => {
      throw new JwtTokenError("TOKEN_EXPIRED", "expired");
    });
    const response = await request(app)
      .post("/api/v1/auth/refresh")
      .send({ refreshToken: "expired-refresh" });

    expect(response.status).toBe(401);
    expect(response.body.error.code).toBe("invalid_token");
  });

  it("logs out the session identified by the refresh token", async () => {
    const { app, sessions } = testApp();
    const response = await request(app)
      .post("/api/v1/auth/logout")
      .send({ refreshToken: "current-refresh" });

    expect(response.status).toBe(204);
    expect(sessions.logout).toHaveBeenCalledWith("session-1");
  });

  it("logs out every session using an authenticated access token", async () => {
    const { app, sessions, jwt } = testApp();
    const response = await request(app)
      .post("/api/v1/auth/logout-all")
      .set("Authorization", "Bearer access-token");

    expect(response.status).toBe(204);
    expect(jwt.verifyAccessToken).toHaveBeenCalledWith("access-token");
    expect(sessions.logoutAllDevices).toHaveBeenCalledWith("user-1");
  });

  it("requires a bearer token for logout-all", async () => {
    const { app, sessions } = testApp();
    const response = await request(app).post("/api/v1/auth/logout-all");

    expect(response.status).toBe(401);
    expect(sessions.logoutAllDevices).not.toHaveBeenCalled();
  });
});
