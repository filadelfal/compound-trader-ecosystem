export type SessionErrorCode =
  | "SESSION_NOT_FOUND"
  | "SESSION_REVOKED"
  | "SESSION_EXPIRED"
  | "REFRESH_TOKEN_NOT_FOUND"
  | "REFRESH_TOKEN_INVALID"
  | "REFRESH_TOKEN_REUSED";

export class SessionSecurityError extends Error {
  public readonly code: SessionErrorCode;

  public constructor(
    code: SessionErrorCode,
    message: string,
    options?: ErrorOptions,
  ) {
    super(message, options);

    this.name = "SessionSecurityError";
    this.code = code;
  }
}
