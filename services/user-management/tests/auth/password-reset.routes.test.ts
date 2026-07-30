import express from "express";
import request from "supertest";

import { createPasswordResetRouter } from "../../src/auth/password-reset/password-reset.routes";
import { PasswordResetTokenError } from "../../src/auth/password-reset/password-reset.service";
import { PasswordPolicyError } from "../../src/auth/password/password.service";

function testApp(service: {
  requestReset: jest.Mock;
  resetPassword: jest.Mock;
}) {
  const app = express();
  app.use(express.json());
  app.use("/api/v1/auth", createPasswordResetRouter(service as never));
  app.use((
    _error: unknown,
    _req: express.Request,
    response: express.Response,
    _next: express.NextFunction,
  ) => {
    response.status(500).json({ error: { code: "internal_error" } });
  });
  return app;
}

describe("password reset routes", () => {
  it("accepts a forgot-password request without disclosing account existence", async () => {
    const service = {
      requestReset: jest.fn().mockResolvedValue(undefined),
      resetPassword: jest.fn(),
    };
    const response = await request(testApp(service))
      .post("/api/v1/auth/forgot-password")
      .send({ email: "user@example.com" });

    expect(response.status).toBe(202);
    expect(service.requestReset).toHaveBeenCalledWith(
      "user@example.com",
      expect.any(String),
    );
  });

  it("rejects invalid forgot-password input", async () => {
    const service = { requestReset: jest.fn(), resetPassword: jest.fn() };
    const response = await request(testApp(service))
      .post("/api/v1/auth/forgot-password")
      .send({ email: "invalid" });

    expect(response.status).toBe(400);
    expect(response.body.error.code).toBe("validation_error");
  });

  it("resets a password with a valid token", async () => {
    const service = {
      requestReset: jest.fn(),
      resetPassword: jest.fn().mockResolvedValue(undefined),
    };
    const response = await request(testApp(service))
      .post("/api/v1/auth/reset-password")
      .send({ token: "a".repeat(43), newPassword: "SecurePassword123!" });

    expect(response.status).toBe(200);
    expect(response.body.data.passwordReset).toBe(true);
  });

  it("rejects an invalid or expired reset token", async () => {
    const service = {
      requestReset: jest.fn(),
      resetPassword: jest.fn().mockRejectedValue(new PasswordResetTokenError()),
    };
    const response = await request(testApp(service))
      .post("/api/v1/auth/reset-password")
      .send({ token: "a".repeat(43), newPassword: "SecurePassword123!" });

    expect(response.status).toBe(400);
    expect(response.body.error.code).toBe("invalid_password_reset_token");
  });

  it("returns password-policy errors", async () => {
    const service = {
      requestReset: jest.fn(),
      resetPassword: jest.fn().mockRejectedValue(
        new PasswordPolicyError(["Password is too common."]),
      ),
    };
    const response = await request(testApp(service))
      .post("/api/v1/auth/reset-password")
      .send({ token: "a".repeat(43), newPassword: "long-but-rejected" });

    expect(response.status).toBe(400);
    expect(response.body.error.code).toBe("password_policy_failed");
  });
});
