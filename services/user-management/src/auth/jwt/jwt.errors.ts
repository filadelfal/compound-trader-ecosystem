export type JwtErrorCode =
  | "TOKEN_EXPIRED"
  | "TOKEN_NOT_ACTIVE"
  | "TOKEN_INVALID"
  | "TOKEN_TYPE_INVALID"
  | "TOKEN_CLAIMS_INVALID";

export class JwtTokenError extends Error {
  public readonly code: JwtErrorCode;

  public constructor(
    code: JwtErrorCode,
    message: string,
    options?: ErrorOptions,
  ) {
    super(message, options);

    this.name = "JwtTokenError";
    this.code = code;
  }
}
