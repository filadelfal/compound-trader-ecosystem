import express from "express";
import request from "supertest";

import { authenticate, requireRoles } from "../../src/auth/authorization/authorization.middleware";

describe("authorization middleware", () => {
  it("allows a server-verified permitted role", async () => {
    const jwt = { verifyAccessToken: jest.fn().mockReturnValue({
      subject: "user-1", session_id: "session-1", roles: ["admin"],
    }) };
    const app = express();
    app.get("/admin", authenticate(jwt as never), requireRoles("admin"), (_req, res) => {
      res.status(200).json({ ok: true });
    });
    expect((await request(app).get("/admin").set("Authorization", "Bearer token")).status).toBe(200);
  });

  it("forbids an authenticated user without the required role", async () => {
    const jwt = { verifyAccessToken: jest.fn().mockReturnValue({
      subject: "user-1", session_id: "session-1", roles: ["user"],
    }) };
    const app = express();
    app.get("/admin", authenticate(jwt as never), requireRoles("admin"), (_req, res) => {
      res.status(200).send();
    });
    expect((await request(app).get("/admin").set("Authorization", "Bearer token")).status).toBe(403);
  });
});
