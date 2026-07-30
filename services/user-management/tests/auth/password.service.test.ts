import {
  PasswordPolicyError,
  PasswordService,
} from "../../src/auth/password/password.service";

describe("PasswordService", () => {
  const service = new PasswordService({
    memoryCost: 8192,
    timeCost: 1,
    parallelism: 1,
    hashLength: 32,
  });

  it("hashes and verifies a valid password", async () => {
    const password = "StrongPassword123!";

    const hash = await service.hash(password);

    expect(hash).not.toBe(password);
    expect(hash.startsWith("$argon2id$")).toBe(true);
    await expect(service.verify(hash, password)).resolves.toBe(true);
  });

  it("rejects an incorrect password", async () => {
    const hash = await service.hash("StrongPassword123!");

    await expect(
      service.verify(hash, "IncorrectPassword123!"),
    ).resolves.toBe(false);
  });

  it("rejects passwords that violate policy", async () => {
    await expect(service.hash("weak")).rejects.toBeInstanceOf(
      PasswordPolicyError,
    );
  });

  it("returns false for a corrupted hash", async () => {
    await expect(
      service.verify("not-an-argon2-hash", "StrongPassword123!"),
    ).resolves.toBe(false);
  });

  it("generates different salts for identical passwords", async () => {
    const password = "StrongPassword123!";

    const firstHash = await service.hash(password);
    const secondHash = await service.hash(password);

    expect(firstHash).not.toBe(secondHash);
    await expect(service.verify(firstHash, password)).resolves.toBe(true);
    await expect(service.verify(secondHash, password)).resolves.toBe(true);
  });
});



