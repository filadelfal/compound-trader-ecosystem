import express from "express";
import request from "supertest";

import { createRegistrationRouter } from "../../src/auth/registration/registration.routes";
import {
  RegistrationConflictError,
  VerificationTokenError,
} from "../../src/auth/registration/registration.service";

function testApp(service: {
  register: jest.Mock;
  verifyEmail: jest.Mock;
}) {
  const app = express();
  app.use(express.json());
  app.use("/api/v1/auth", createRegistrationRouter(service as never));
  app.use((_error: unknown, _req: express.Request, res: express.Response, _next: express.NextFunction) => {
    res.status(500).json({ error: { code: "internal_error" } });
  });
  return app;
}

describe("registration routes", () => {
  it("registers a valid user", async () => {
    const service = {
      register: jest.fn().mockResolvedValue({ userId: "user-1" }),
      verifyEmail: jest.fn(),
    };
    const response = await request(testApp(service))
      .post("/api/v1/auth/register")
      .send({ email: "USER@example.com", password: "SecurePassword123!" });

    expect(response.status).toBe(201);
    expect(response.body.data.verificationRequired).toBe(true);
    expect(service.register).toHaveBeenCalled();
  });

  it("rejects invalid registration input", async () => {
    const service = { register: jest.fn(), verifyEmail: jest.fn() };
    const response = await request(testApp(service))
      .post("/api/v1/auth/register")
      .send({ email: "invalid", password: "short" });

    expect(response.status).toBe(400);
    expect(response.body.error.code).toBe("validation_error");
  });

  it("returns conflict without exposing account details", async () => {
    const service = {
      register: jest.fn().mockRejectedValue(new RegistrationConflictError()),
      verifyEmail: jest.fn(),
    };
    const response = await request(testApp(service))
      .post("/api/v1/auth/register")
      .send({ email: "user@example.com", password: "SecurePassword123!" });

    expect(response.status).toBe(409);
    expect(response.body.error.code).toBe("email_already_registered");
  });

  it("verifies a valid token", async () => {
    const service = {
      register: jest.fn(),
      verifyEmail: jest.fn().mockResolvedValue(undefined),
    };
    const response = await request(testApp(service))
      .post("/api/v1/auth/verify-email")
      .send({ token: "a".repeat(43) });

    expect(response.status).toBe(200);
    expect(response.body.data.verified).toBe(true);
  });

  it("rejects an expired token", async () => {
    const service = {
      register: jest.fn(),
      verifyEmail: jest.fn().mockRejectedValue(new VerificationTokenError()),
    };
    const response = await request(testApp(service))
      .post("/api/v1/auth/verify-email")
      .send({ token: "a".repeat(43) });

    expect(response.status).toBe(400);
    expect(response.body.error.code).toBe("invalid_verification_token");
  });
});
