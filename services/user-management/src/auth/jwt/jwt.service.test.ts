import { JwtService } from "./jwt.service";
import { JwtTokenError } from "./jwt.errors";
import type { JwtServiceOptions } from "./jwt.types";

const TEST_ACCESS_SECRET =
  "access-test-secret-0123456789abcdef-access-test-secret-0123456789abcdef";

const TEST_REFRESH_SECRET =
  "refresh-test-secret-0123456789abcdef-refresh-test-secret-0123456789abcdef";

function createOptions(
  overrides: Partial<JwtServiceOptions> = {},
): JwtServiceOptions {
  return {
    accessSecret: TEST_ACCESS_SECRET,
    refreshSecret: TEST_REFRESH_SECRET,
    issuer: "compound-trader-test",
    audience: "compound-trader-test-services",
    accessExpiresIn: "15m",
    refreshExpiresIn: "7d",
    ...overrides,
  };
}

describe("JwtService", () => {
  it("signs and verifies an access token", () => {
    const service = new JwtService(createOptions());

    const token = service.signAccessToken({
      userId: "user-001",
      sessionId: "session-001",
      roles: ["trader", "admin", "trader"],
    });

    const claims = service.verifyAccessToken(token);

    expect(claims.subject).toBe("user-001");
    expect(claims.session_id).toBe("session-001");
    expect(claims.token_use).toBe("access");
    expect(claims.roles).toEqual(["trader", "admin"]);
    expect(claims.expiresAt).toBeGreaterThan(claims.issuedAt);
  });

  it("signs and verifies a refresh token", () => {
    const service = new JwtService(createOptions());

    const token = service.signRefreshToken({
      userId: "user-002",
      sessionId: "session-002",
      tokenId: "refresh-token-002",
    });

    const claims = service.verifyRefreshToken(token);

    expect(claims.subject).toBe("user-002");
    expect(claims.session_id).toBe("session-002");
    expect(claims.tokenId).toBe("refresh-token-002");
    expect(claims.token_use).toBe("refresh");
  });

  it("issues a complete token pair", () => {
    const service = new JwtService(createOptions());

    const pair = service.issueTokenPair(
      {
        userId: "user-003",
        sessionId: "session-003",
        roles: ["trader"],
      },
      "refresh-token-003",
    );

    expect(typeof pair.accessToken).toBe("string");
    expect(typeof pair.refreshToken).toBe("string");

    expect(
      service.verifyAccessToken(pair.accessToken).subject,
    ).toBe("user-003");

    expect(
      service.verifyRefreshToken(pair.refreshToken).tokenId,
    ).toBe("refresh-token-003");
  });

  it("rejects a refresh token used as an access token", () => {
    const service = new JwtService(createOptions());

    const refreshToken = service.signRefreshToken({
      userId: "user-004",
      sessionId: "session-004",
      tokenId: "refresh-token-004",
    });

    expect(() =>
      service.verifyAccessToken(refreshToken),
    ).toThrow(JwtTokenError);

    try {
      service.verifyAccessToken(refreshToken);
    } catch (error) {
      expect(error).toBeInstanceOf(JwtTokenError);
      expect((error as JwtTokenError).code).toBe(
        "TOKEN_INVALID",
      );
    }
  });

  it("rejects a tampered access token", () => {
    const service = new JwtService(createOptions());

    const token = service.signAccessToken({
      userId: "user-005",
      sessionId: "session-005",
    });

    const tamperedToken =
      token.substring(0, token.length - 1) +
      (token.endsWith("a") ? "b" : "a");

    expect(() =>
      service.verifyAccessToken(tamperedToken),
    ).toThrow(JwtTokenError);
  });

  it("rejects an expired access token", () => {
    const service = new JwtService(
      createOptions({
        accessExpiresIn: 0,
      }),
    );

    const token = service.signAccessToken({
      userId: "user-006",
      sessionId: "session-006",
    });

    try {
      service.verifyAccessToken(token);
      throw new Error("Expected token verification to fail");
    } catch (error) {
      expect(error).toBeInstanceOf(JwtTokenError);
      expect((error as JwtTokenError).code).toBe(
        "TOKEN_EXPIRED",
      );
    }
  });

  it("rejects tokens from another issuer", () => {
    const issuerA = new JwtService(
      createOptions({
        issuer: "issuer-a",
      }),
    );

    const issuerB = new JwtService(
      createOptions({
        issuer: "issuer-b",
      }),
    );

    const token = issuerA.signAccessToken({
      userId: "user-007",
      sessionId: "session-007",
    });

    expect(() =>
      issuerB.verifyAccessToken(token),
    ).toThrow(JwtTokenError);
  });

  it("decodes token metadata without verification", () => {
    const service = new JwtService(createOptions());

    const token = service.signAccessToken({
      userId: "user-008",
      sessionId: "session-008",
      roles: ["trader"],
    });

    const decoded = service.decode(token);

    expect(decoded?.sub).toBe("user-008");
    expect(decoded?.token_use).toBe("access");
    expect(decoded?.session_id).toBe("session-008");
  });
});









