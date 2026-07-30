export interface PasswordPolicyResult {
  valid: boolean;
  errors: string[];
}

export interface PasswordPolicyOptions {
  minimumLength: number;
  maximumLength: number;
  requireUppercase: boolean;
  requireLowercase: boolean;
  requireNumber: boolean;
  requireSpecialCharacter: boolean;
}

export const defaultPasswordPolicy: PasswordPolicyOptions = {
  minimumLength: 12,
  maximumLength: 128,
  requireUppercase: true,
  requireLowercase: true,
  requireNumber: true,
  requireSpecialCharacter: true,
};

const COMMON_PASSWORDS = new Set([
  "password",
  "password123",
  "12345678",
  "123456789",
  "qwerty123",
  "admin123",
  "letmein",
  "welcome123",
]);

export function validatePasswordPolicy(
  password: string,
  options: PasswordPolicyOptions = defaultPasswordPolicy,
): PasswordPolicyResult {
  const errors: string[] = [];

  if (typeof password !== "string") {
    return {
      valid: false,
      errors: ["Password must be a string."],
    };
  }

  if (password.length < options.minimumLength) {
    errors.push(
      `Password must contain at least ${options.minimumLength} characters.`,
    );
  }

  if (password.length > options.maximumLength) {
    errors.push(
      `Password must contain no more than ${options.maximumLength} characters.`,
    );
  }

  if (options.requireUppercase && !/[A-Z]/.test(password)) {
    errors.push("Password must contain an uppercase letter.");
  }

  if (options.requireLowercase && !/[a-z]/.test(password)) {
    errors.push("Password must contain a lowercase letter.");
  }

  if (options.requireNumber && !/[0-9]/.test(password)) {
    errors.push("Password must contain a number.");
  }

  if (
    options.requireSpecialCharacter &&
    !/[^A-Za-z0-9\s]/.test(password)
  ) {
    errors.push("Password must contain a special character.");
  }

  if (/\s/.test(password)) {
    errors.push("Password must not contain whitespace.");
  }

  if (COMMON_PASSWORDS.has(password.toLowerCase())) {
    errors.push("Password is too common.");
  }

  return {
    valid: errors.length === 0,
    errors,
  };
}
