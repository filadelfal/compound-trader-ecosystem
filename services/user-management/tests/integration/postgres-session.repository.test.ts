
import {
  randomBytes,
  randomUUID,
} from "node:crypto";

import {
  readFile,
} from "node:fs/promises";

import {
  resolve,
} from "node:path";

import {
  Pool,
} from "pg";

import {
  PostgresSessionRepository,
} from "../../src/auth/session";

import type {
  StoredRefreshToken,
  UserSession,
} from "../../src/auth/session";

const databaseUrl =
  process.env.TEST_DATABASE_URL ?? "";

const describeDatabase =
  databaseUrl.length > 0
    ? describe
    : describe.skip;

describeDatabase(
  "PostgresSessionRepository",
  () => {
    let database: Pool;
    let repository:
      PostgresSessionRepository;

    beforeAll(async () => {
      database = new Pool({
        connectionString: databaseUrl,
      });

      const migration = await readFile(
        resolve(
          process.cwd(),
          "migrations",
          "003_auth_sessions.sql",
        ),
        "utf8",
      );

      await database.query(migration);

      repository =
        new PostgresSessionRepository(
          database,
        );
    });

    beforeEach(async () => {
      await database.query(`
        TRUNCATE TABLE
          authentication_security_events,
          refresh_tokens,
          user_sessions
        CASCADE
      `);
    });

    afterAll(async () => {
      if (database) {
        await database.end();
      }
    });

    function createUniqueTokenHash(): string {
      return randomBytes(32).toString("hex");
    }

    function createSessionRecord(
      userId: string,
    ): {
      session: UserSession;
      token: StoredRefreshToken;
    } {
      const now = new Date();
      const sessionId = randomUUID();
      const tokenId = randomUUID();

      const session: UserSession = {
        id: sessionId,
        userId,
        status: "active",
        metadata: {
          deviceName: "Integration test",
          ipAddress: "127.0.0.1",
        },
        createdAt: now,
        lastActivityAt: now,
        expiresAt: new Date(
          now.getTime() +
            60 * 60 * 1000,
        ),
      };

      const token: StoredRefreshToken = {
        id: tokenId,
        sessionId,
        userId,
        tokenHash:
          createUniqueTokenHash(),
        status: "active",
        createdAt: now,
        expiresAt: session.expiresAt,
      };

      return {
        session,
        token,
      };
    }

    it(
      "creates and retrieves a session",
      async () => {
        const userId = randomUUID();

        const record =
          createSessionRecord(userId);

        await repository.createSession(
          record.session,
          record.token,
        );

        const found =
          await repository.findSessionById(
            record.session.id,
          );

        expect(found?.id).toBe(
          record.session.id,
        );

        expect(found?.userId).toBe(
          userId,
        );

        const storedToken =
          await repository
            .findRefreshTokenById(
              record.token.id,
            );

        expect(
          storedToken?.tokenHash,
        ).toBe(
          record.token.tokenHash,
        );
      },
    );

    it(
      "rotates a refresh token atomically",
      async () => {
        const userId = randomUUID();

        const record =
          createSessionRecord(userId);

        await repository.createSession(
          record.session,
          record.token,
        );

        const replacement:
          StoredRefreshToken = {
            ...record.token,
            id: randomUUID(),
            tokenHash:
              createUniqueTokenHash(),
            createdAt: new Date(),
          };

        const result =
          await repository
            .rotateRefreshTokenAtomically({
              sessionId:
                record.session.id,
              userId,
              currentTokenId:
                record.token.id,
              currentTokenHash:
                record.token.tokenHash,
              replacementToken:
                replacement,
              activityAt: new Date(),
            });

        expect(result).toBe(
          "rotated",
        );

        const previous =
          await repository
            .findRefreshTokenById(
              record.token.id,
            );

        expect(previous?.status).toBe(
          "rotated",
        );

        expect(
          previous?.replacedByTokenId,
        ).toBe(
          replacement.id,
        );
      },
    );

    it(
      "detects reuse of a rotated token",
      async () => {
        const userId = randomUUID();

        const record =
          createSessionRecord(userId);

        await repository.createSession(
          record.session,
          record.token,
        );

        const replacement:
          StoredRefreshToken = {
            ...record.token,
            id: randomUUID(),
            tokenHash:
              createUniqueTokenHash(),
            createdAt: new Date(),
          };

        const rotationInput = {
          sessionId:
            record.session.id,
          userId,
          currentTokenId:
            record.token.id,
          currentTokenHash:
            record.token.tokenHash,
          replacementToken:
            replacement,
          activityAt:
            new Date(),
        };

        expect(
          await repository
            .rotateRefreshTokenAtomically(
              rotationInput,
            ),
        ).toBe(
          "rotated",
        );

        expect(
          await repository
            .rotateRefreshTokenAtomically({
              ...rotationInput,
              replacementToken: {
                ...replacement,
                id: randomUUID(),
                tokenHash:
                  createUniqueTokenHash(),
              },
            }),
        ).toBe(
          "token_reused",
        );

        const session =
          await repository.findSessionById(
            record.session.id,
          );

        expect(session?.status).toBe(
          "compromised",
        );
      },
    );

    it(
      "revokes all active user sessions",
      async () => {
        const userId = randomUUID();

        const first =
          createSessionRecord(userId);

        const second =
          createSessionRecord(userId);

        await repository.createSession(
          first.session,
          first.token,
        );

        await repository.createSession(
          second.session,
          second.token,
        );

        const revoked =
          await repository
            .revokeAllUserSessions(
              userId,
              "integration_test",
              new Date(),
            );

        expect(revoked).toBe(2);

        const active =
          await repository
            .listActiveSessions(
              userId,
              new Date(),
            );

        expect(active).toHaveLength(0);
      },
    );
  },
);
