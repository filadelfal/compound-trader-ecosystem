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
  const newPassword = "ChangedIntegrationPassword456!";
  let userId = "";

  async function latestEmailToken(subject: string): Promise<string> {
    const queued = await database.query<{ text_body: string }>(
      `SELECT text_body
         FROM compound.email_outbox
        WHERE recipient = $1 AND subject = $2
        ORDER BY created_at DESC
        LIMIT 1`,
      [email, subject],
    );
    const token = queued.rows[0]?.text_body.match(/[?&]token=([^\s&]+)/)?.[1];
    expect(token).toBeDefined();
    return decodeURIComponent(token!);
  }

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

  it("exercises every authentication route and persists security evidence", async () => {
    const registration = await request(app).post("/api/v1/auth/register")
      .send({ email, password, displayName: "Integration User" });
    expect(registration.status).toBe(201);
    userId = registration.body.data.userId;

    const firstVerificationToken = await latestEmailToken("Verify your email address");
    const resend = await request(app).post("/api/v1/auth/resend-verification")
      .send({ email });
    expect(resend.status).toBe(202);
    const verificationToken = await latestEmailToken("Verify your email address");
    expect(verificationToken).not.toBe(firstVerificationToken);

    const obsoleteVerification = await request(app).post("/api/v1/auth/verify-email")
      .send({ token: firstVerificationToken });
    expect(obsoleteVerification.status).toBe(400);

    const verification = await request(app).post("/api/v1/auth/verify-email")
      .send({ token: verificationToken });
    expect(verification.status).toBe(200);

    const login = await request(app).post("/api/v1/auth/login").send({ email, password });
    expect(login.status).toBe(200);

    const update = await request(app).patch("/api/v1/users/me")
      .set("authorization", `Bearer ${login.body.data.accessToken}`)
      .send({
        displayName: "Updated Integration User",
        firstName: "Integration",
        countryCode: "ng",
        timezone: "Africa/Lagos",
      });
    expect(update.status).toBe(200);
    expect(update.body.data.display_name).toBe("Updated Integration User");
    expect(update.body.data.first_name).toBe("Integration");
    expect(update.body.data.country_code).toBe("NG");

    const profile = await request(app).get("/api/v1/users/me")
      .set("authorization", `Bearer ${login.body.data.accessToken}`);
    expect(profile.status).toBe(200);
    expect(profile.body.data.email).toBe(email);

    const refresh = await request(app).post("/api/v1/auth/refresh")
      .send({ refreshToken: login.body.data.refreshToken });
    expect(refresh.status).toBe(200);
    expect(refresh.body.data.refreshToken).not.toBe(login.body.data.refreshToken);

    const logout = await request(app).post("/api/v1/auth/logout")
      .send({ refreshToken: refresh.body.data.refreshToken });
    expect(logout.status).toBe(204);
    const refreshAfterLogout = await request(app).post("/api/v1/auth/refresh")
      .send({ refreshToken: refresh.body.data.refreshToken });
    expect(refreshAfterLogout.status).toBe(401);

    const passwordRecoverySession = await request(app).post("/api/v1/auth/login")
      .send({ email, password });
    expect(passwordRecoverySession.status).toBe(200);
    const forgotPassword = await request(app).post("/api/v1/auth/forgot-password")
      .send({ email });
    expect(forgotPassword.status).toBe(202);
    const resetToken = await latestEmailToken("Reset your password");
    const resetPassword = await request(app).post("/api/v1/auth/reset-password")
      .send({ token: resetToken, newPassword });
    expect(resetPassword.status).toBe(200);

    const revokedAfterReset = await request(app).post("/api/v1/auth/refresh")
      .send({ refreshToken: passwordRecoverySession.body.data.refreshToken });
    expect(revokedAfterReset.status).toBe(401);
    const oldPasswordLogin = await request(app).post("/api/v1/auth/login")
      .send({ email, password });
    expect(oldPasswordLogin.status).toBe(401);
    const newPasswordLogin = await request(app).post("/api/v1/auth/login")
      .send({ email, password: newPassword });
    expect(newPasswordLogin.status).toBe(200);

    const logoutAll = await request(app).post("/api/v1/auth/logout-all")
      .set("authorization", `Bearer ${newPasswordLogin.body.data.accessToken}`);
    expect(logoutAll.status).toBe(204);
    const refreshAfterLogoutAll = await request(app).post("/api/v1/auth/refresh")
      .send({ refreshToken: newPasswordLogin.body.data.refreshToken });
    expect(refreshAfterLogoutAll.status).toBe(401);

    for (let attempt = 1; attempt <= 5; attempt += 1) {
      const failed = await request(app).post("/api/v1/auth/login")
        .send({ email, password: "DefinitelyWrongPassword!" });
      expect(failed.status).toBe(attempt >= 4 ? 423 : 401);
    }

    const lockedUser = await database.query<{
      failed_login_attempts: number;
      locked_until: Date | null;
      status: string;
    }>(
      `SELECT failed_login_attempts, locked_until, status
         FROM compound.users
        WHERE id = $1`,
      [userId],
    );
    expect(lockedUser.rows[0]).toMatchObject({
      failed_login_attempts: 5,
      status: "locked",
    });
    expect(lockedUser.rows[0].locked_until).not.toBeNull();

    const attemptEvidence = await database.query<{
      successful: boolean;
      failure_reason: string | null;
    }>(
      `SELECT successful, failure_reason
         FROM compound.login_attempts
        WHERE user_id = $1
        ORDER BY attempted_at`,
      [userId],
    );
    expect(attemptEvidence.rows.some(({ successful }) => successful)).toBe(true);
    expect(
      attemptEvidence.rows.some(({ failure_reason }) => failure_reason === "account_locked"),
    ).toBe(true);

    const auditEvidence = await database.query<{ event_type: string }>(
      `SELECT event_type
         FROM compound.audit_logs
        WHERE user_id = $1`,
      [userId],
    );
    expect(auditEvidence.rows.map(({ event_type }) => event_type)).toEqual(
      expect.arrayContaining([
        "auth.registration.created",
        "auth.email.verification_resent",
        "auth.email.verified",
        "auth.login.succeeded",
        "user.profile_updated",
        "auth.password_reset.requested",
        "auth.password_reset.completed",
        "auth.account.locked",
      ]),
    );
  });
});
