/**
 * Safe environment configuration for automated tests only.
 * These values are not production credentials.
 */

process.env.NODE_ENV = "test";
process.env.PORT = "3002";
process.env.SERVICE_NAME = "user-management";
process.env.LOG_LEVEL = "silent";

process.env.DATABASE_URL =
  "postgresql://test_user:test_password@localhost:5432/user_management_test";

process.env.REDIS_URL = "redis://localhost:6379/15";

process.env.JWT_ACCESS_SECRET =
  "test-access-secret-that-is-long-enough-0123456789abcdef";

process.env.JWT_REFRESH_SECRET =
  "test-refresh-secret-that-is-long-enough-0123456789abcdef";

process.env.JWT_ISSUER = "compound-trader-test";
process.env.JWT_AUDIENCE = "compound-trader-test-services";
process.env.JWT_ACCESS_EXPIRES_IN = "15m";
process.env.JWT_REFRESH_EXPIRES_DAYS = "30";

process.env.EMAIL_VERIFICATION_EXPIRES_HOURS = "24";
process.env.PASSWORD_RESET_EXPIRES_MINUTES = "30";

process.env.ARGON2_MEMORY_COST = "8192";
process.env.ARGON2_TIME_COST = "1";
process.env.ARGON2_PARALLELISM = "1";

process.env.ACCOUNT_LOCK_THRESHOLD = "5";
process.env.ACCOUNT_LOCK_MINUTES = "15";
