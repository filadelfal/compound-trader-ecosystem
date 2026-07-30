import request from "supertest";
import { app } from "../src/app";

describe("health endpoint", () => {
  it("returns service health", async () => {
    const response = await request(app).get("/health");
    expect(response.status).toBe(200);
    expect(response.body.status).toBe("ok");
  });
});
