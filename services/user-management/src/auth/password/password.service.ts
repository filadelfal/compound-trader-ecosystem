import argon2 from "argon2";

import {
  defaultPasswordPolicy,
  PasswordPolicyOptions,
  validatePasswordPolicy,
} from "./password.policy";

export interface PasswordHashingOptions {
  memoryCost: number;
  timeCost: number;
  parallelism: number;
  hashLength: number;
}

export class PasswordPolicyError extends Error {
  public readonly errors: string[];

  public constructor(errors: string[]) {
    super("Password does not satisfy the security policy.");
    this.name = "PasswordPolicyError";
    this.errors = errors;
  }
}

function readPositiveInteger(
  value: string | undefined,
  fallback: number,
): number {
  if (!value) {
    return fallback;
  }

  const parsed = Number.parseInt(value, 10);

  if (!Number.isSafeInteger(parsed) || parsed <= 0) {
    return fallback;
  }

  return parsed;
}

export function getPasswordHashingOptions(): PasswordHashingOptions {
  return {
    memoryCost: readPositiveInteger(
      process.env.ARGON2_MEMORY_COST,
      65536,
    ),
    timeCost: readPositiveInteger(
      process.env.ARGON2_TIME_COST,
      3,
    ),
    parallelism: readPositiveInteger(
      process.env.ARGON2_PARALLELISM,
      1,
    ),
    hashLength: 32,
  };
}

export class PasswordService {
  public constructor(
    private readonly hashingOptions: PasswordHashingOptions =
      getPasswordHashingOptions(),
    private readonly policyOptions: PasswordPolicyOptions =
      defaultPasswordPolicy,
  ) {}

  public async hash(password: string): Promise<string> {
    const policyResult = validatePasswordPolicy(
      password,
      this.policyOptions,
    );

    if (!policyResult.valid) {
      throw new PasswordPolicyError(policyResult.errors);
    }

    return argon2.hash(password, {
      type: argon2.argon2id,
      memoryCost: this.hashingOptions.memoryCost,
      timeCost: this.hashingOptions.timeCost,
      parallelism: this.hashingOptions.parallelism,
      hashLength: this.hashingOptions.hashLength,
    });
  }

  public async verify(
    storedHash: string,
    suppliedPassword: string,
  ): Promise<boolean> {
    if (!storedHash || !suppliedPassword) {
      return false;
    }

    try {
      return await argon2.verify(storedHash, suppliedPassword);
    } catch {
      // Invalid or corrupted hashes must be treated as failed authentication.
      return false;
    }
  }

  public needsRehash(storedHash: string): boolean {
    if (!storedHash) {
      return true;
    }

    try {
      return argon2.needsRehash(storedHash, {
        memoryCost: this.hashingOptions.memoryCost,
        timeCost: this.hashingOptions.timeCost,
        parallelism: this.hashingOptions.parallelism,
      });
    } catch {
      return true;
    }
  }

  public validate(password: string): void {
    const result = validatePasswordPolicy(
      password,
      this.policyOptions,
    );

    if (!result.valid) {
      throw new PasswordPolicyError(result.errors);
    }
  }
}

export const passwordService = new PasswordService();

