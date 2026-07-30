export type SessionStatus =
  | "active"
  | "revoked"
  | "expired"
  | "compromised";

export type RefreshTokenStatus =
  | "active"
  | "rotated"
  | "revoked"
  | "expired";

export interface SessionDeviceMetadata {
  deviceId?: string;
  deviceName?: string;
  browser?: string;
  operatingSystem?: string;
  ipAddress?: string;
  userAgent?: string;
  countryCode?: string;
}

export interface UserSession {
  id: string;
  userId: string;
  status: SessionStatus;
  metadata: SessionDeviceMetadata;
  createdAt: Date;
  lastActivityAt: Date;
  expiresAt: Date;
  revokedAt?: Date;
  revocationReason?: string;
}

export interface StoredRefreshToken {
  id: string;
  sessionId: string;
  userId: string;
  tokenHash: string;
  status: RefreshTokenStatus;
  createdAt: Date;
  expiresAt: Date;
  rotatedAt?: Date;
  replacedByTokenId?: string;
  revokedAt?: Date;
}

export interface CreateSessionInput {
  userId: string;
  roles?: string[];
  metadata?: SessionDeviceMetadata;
}

export interface CreatedSession {
  session: UserSession;
  accessToken: string;
  refreshToken: string;
}

export interface RotatedSessionTokens {
  sessionId: string;
  userId: string;
  accessToken: string;
  refreshToken: string;
}

export type RotationResult =
  | "rotated"
  | "token_missing"
  | "token_mismatch"
  | "token_reused"
  | "session_missing"
  | "session_revoked"
  | "session_expired";

export interface AtomicRotationInput {
  sessionId: string;
  userId: string;
  currentTokenId: string;
  currentTokenHash: string;
  replacementToken: StoredRefreshToken;
  activityAt: Date;
}

export interface SessionRepository {
  createSession(
    session: UserSession,
    refreshToken: StoredRefreshToken,
  ): Promise<void>;

  findSessionById(
    sessionId: string,
  ): Promise<UserSession | null>;

  findRefreshTokenById(
    tokenId: string,
  ): Promise<StoredRefreshToken | null>;

  rotateRefreshTokenAtomically(
    input: AtomicRotationInput,
  ): Promise<RotationResult>;

  revokeSession(
    sessionId: string,
    reason: string,
    revokedAt: Date,
  ): Promise<boolean>;

  revokeAllUserSessions(
    userId: string,
    reason: string,
    revokedAt: Date,
  ): Promise<number>;

  listActiveSessions(
    userId: string,
    now: Date,
  ): Promise<UserSession[]>;
}
