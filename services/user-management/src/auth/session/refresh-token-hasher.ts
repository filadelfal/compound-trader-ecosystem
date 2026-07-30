import {
  createHash,
  timingSafeEqual,
} from "node:crypto";

export class RefreshTokenHasher {
  public hash(token: string): string {
    return createHash("sha256")
      .update(token, "utf8")
      .digest("hex");
  }

  public matches(
    token: string,
    expectedHash: string,
  ): boolean {
    const actualHash = this.hash(token);

    const actualBuffer = Buffer.from(actualHash, "hex");
    const expectedBuffer = Buffer.from(expectedHash, "hex");

    if (actualBuffer.length !== expectedBuffer.length) {
      return false;
    }

    return timingSafeEqual(
      actualBuffer,
      expectedBuffer,
    );
  }
}

export const refreshTokenHasher =
  new RefreshTokenHasher();
