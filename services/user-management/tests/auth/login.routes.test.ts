import express from "express";
import request from "supertest";

import { createLoginRouter } from "../../src/auth/login/login.routes";
import {
  AccountLockedError,
  InvalidCredentialsError,
} from "../../src/auth/login/login.service";

function testApp(login: jest.Mock) {
  const app = express();
  app.use(express.json());
  app.use("/api/v1/auth", createLoginRouter({ login } as never));
  app.use((_error: unknown, _request: express.Request, response: express.Response, _next: express.NextFunction) => {
    response.status(500).json({ error: { code: "internal_error" } });
  });
  return app;
}

describe("login route", () => {
  it("returns a bearer token pair for valid credentials", async () => {
    const login = jest.fn().mockResolvedValue({
      accessToken: "access-token",
      refreshToken: "refresh-token",
    });
    const response = await request(testApp(login))
      .post("/api/v1/auth/login")
      .send({ email: "user@example.com", password: "SecurePassword123!" });

    expect(response.status).toBe(200);
    expect(response.body.data).toEqual({
      accessToken: "access-token",
      refreshToken: "refresh-token",
      tokenType: "Bearer",
    });
  });

  it("does not reveal whether the account exists", async () => {
    const login = jest.fn().mockRejectedValue(new InvalidCredentialsError());
    const response = await request(testApp(login))
      .post("/api/v1/auth/login")
      .send({ email: "unknown@example.com", password: "wrong" });

    expect(response.status).toBe(401);
    expect(response.body.error.code).toBe("invalid_credentials");
  });

  it("reports a temporary lock without exposing details", async () => {
    const login = jest.fn().mockRejectedValue(new AccountLockedError());
    const response = await request(testApp(login))
      .post("/api/v1/auth/login")
      .send({ email: "user@example.com", password: "wrong" });

    expect(response.status).toBe(423);
    expect(response.body.error.code).toBe("account_locked");
  });

  it("validates login input", async () => {
    const login = jest.fn();
    const response = await request(testApp(login))
      .post("/api/v1/auth/login")
      .send({ email: "invalid" });

    expect(response.status).toBe(400);
    expect(login).not.toHaveBeenCalled();
  });
});
