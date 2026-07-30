import type { SignOptions } from "jsonwebtoken";

export type JwtTokenUse = "access" | "refresh";

export interface BaseTokenClaims {
  token_use: JwtTokenUse;
  session_id: string;
}

export interface AccessTokenClaims extends BaseTokenClaims {
  token_use: "access";
  roles: string[];
}

export interface RefreshTokenClaims extends BaseTokenClaims {
  token_use: "refresh";
}

export interface VerifiedAccessToken extends AccessTokenClaims {
  subject: string;
  issuedAt: number;
  expiresAt: number;
  tokenId?: string;
}

export interface VerifiedRefreshToken extends RefreshTokenClaims {
  subject: string;
  issuedAt: number;
  expiresAt: number;
  tokenId: string;
}

export interface JwtServiceOptions {
  accessSecret: string;
  refreshSecret: string;
  issuer: string;
  audience: string;
  accessExpiresIn: SignOptions["expiresIn"];
  refreshExpiresIn: SignOptions["expiresIn"];
}

export interface AccessTokenInput {
  userId: string;
  sessionId: string;
  roles?: string[];
}

export interface RefreshTokenInput {
  userId: string;
  sessionId: string;
  tokenId: string;
}

export interface TokenPair {
  accessToken: string;
  refreshToken: string;
  accessTokenExpiresIn: SignOptions["expiresIn"];
  refreshTokenExpiresIn: SignOptions["expiresIn"];
}
