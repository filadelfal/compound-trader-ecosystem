import express from "express";
import request from "supertest";

import { createUserRouter } from "../../src/users/user.routes";

function testApp() {
  const users = {
    getCurrentUser: jest.fn().mockResolvedValue({ id: "user-1", roles: ["user"] }),
    updateCurrentUser: jest.fn().mockResolvedValue({
      id: "user-1",
      display_name: "Updated",
      roles: ["user"],
    }),
  };
  const jwt = {
    verifyAccessToken: jest.fn().mockReturnValue({
      subject: "user-1",
      session_id: "session-1",
      roles: ["user"],
    }),
  };
  const app = express();
  app.use(express.json());
  app.use("/api/v1/users", createUserRouter(users as never, jwt as never));
  app.use((_error: unknown, _request: express.Request, response: express.Response, _next: express.NextFunction) => {
    response.status(500).json({ error: { code: "internal_error" } });
  });
  return { app, users, jwt };
}

describe("current user routes", () => {
  it("returns the authenticated user's profile", async () => {
    const { app, users } = testApp();
    const response = await request(app)
      .get("/api/v1/users/me")
      .set("Authorization", "Bearer access-token");
    expect(response.status).toBe(200);
    expect(users.getCurrentUser).toHaveBeenCalledWith("user-1");
  });

  it("rejects unauthenticated requests", async () => {
    const { app, users } = testApp();
    const response = await request(app).get("/api/v1/users/me");
    expect(response.status).toBe(401);
    expect(users.getCurrentUser).not.toHaveBeenCalled();
  });

  it("updates only validated profile fields", async () => {
    const { app, users } = testApp();
    const response = await request(app)
      .patch("/api/v1/users/me")
      .set("Authorization", "Bearer access-token")
      .send({ displayName: "Updated", countryCode: "ng" });
    expect(response.status).toBe(200);
    expect(users.updateCurrentUser).toHaveBeenCalledWith("user-1", {
      displayName: "Updated",
      countryCode: "NG",
    });
  });

  it("rejects unknown or empty update bodies", async () => {
    const { app, users } = testApp();
    const response = await request(app)
      .patch("/api/v1/users/me")
      .set("Authorization", "Bearer access-token")
      .send({ role: "admin" });
    expect(response.status).toBe(400);
    expect(users.updateCurrentUser).not.toHaveBeenCalled();
  });
});
