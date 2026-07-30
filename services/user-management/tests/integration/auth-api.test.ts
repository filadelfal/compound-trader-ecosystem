import { randomBytes } from "node:crypto";
import { readFile, readdir } from "node:fs/promises";
import { resolve } from "node:path";
import { Pool } from "pg";
import request from "supertest";
import { app } from "../../src/app";

const databaseUrl = process.env.TEST_DATABASE_URL ?? "";
const describeDatabase = databaseUrl ? describe : describe.skip;

describeDatabase("authentication API with PostgreSQL", () => {
  let database: Pool;
  const email = `auth-api-${randomBytes(8).toString("hex")}@example.com`;
  const password = "StrongIntegrationPassword123!";
  let userId = "";

  beforeAll(async () => {
    database = new Pool({ connectionString: databaseUrl });
    const directory = resolve(process.cwd(), "migrations");
    for (const filename of (await readdir(directory)).filter((name) => name.endsWith(".sql")).sort()) {
      await database.query(await readFile(resolve(directory, filename), "utf8"));
    }
  });

  afterAll(async () => {
    if (userId) await database.query("DELETE FROM compound.users WHERE id = $1", [userId]);
    await database.end();
  });

  it("registers, verifies, logs in, refreshes, and reads the user", async () => {
    const registration = await request(app).post("/api/v1/auth/register")
      .send({ email, password, displayName: "Integration User" });
    expect(registration.status).toBe(201);
    userId = registration.body.data.userId;

    const queued = await database.query<{ text_body: string }>(
      `SELECT text_body FROM compound.email_outbox
        WHERE recipient = $1 ORDER BY created_at DESC LIMIT 1`,
      [email],
    );
    const token = queued.rows[0]?.text_body.match(/[?&]token=([^\s&]+)/)?.[1];
    expect(token).toBeDefined();

    const verification = await request(app).post("/api/v1/auth/verify-email")
      .send({ token: decodeURIComponent(token!) });
    expect(verification.status).toBe(200);

    const login = await request(app).post("/api/v1/auth/login").send({ email, password });
    expect(login.status).toBe(200);

    const profile = await request(app).get("/api/v1/users/me")
      .set("authorization", `Bearer ${login.body.data.accessToken}`);
    expect(profile.status).toBe(200);
    expect(profile.body.data.email).toBe(email);

    const refresh = await request(app).post("/api/v1/auth/refresh")
      .send({ refreshToken: login.body.data.refreshToken });
    expect(refresh.status).toBe(200);
    expect(refresh.body.data.refreshToken).not.toBe(login.body.data.refreshToken);
  });
});
