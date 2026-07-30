# AUTH-003 — Refresh Token Rotation and Session Management

## Purpose

AUTH-003 provides the session-security foundation for login,
refresh, logout and multi-device account management.

## Implemented

- Database-backed session model
- SHA-256 refresh-token hashing
- No plaintext refresh-token persistence
- One-time refresh-token rotation
- Rotation-chain tracking
- Refresh-token replay detection
- Automatic session compromise handling
- Current-session logout
- Logout from all devices
- Active-session listing
- PostgreSQL migration
- Repository abstraction for atomic database operations

## Security invariant

A refresh token may be accepted only once.

When a token whose status is already `rotated` is submitted again,
the repository reports `token_reused`. The service then revokes the
whole session and returns `REFRESH_TOKEN_REUSED`.

## Atomic rotation requirement

The PostgreSQL repository must perform these operations in one
transaction:

1. Lock the session row.
2. Lock the current refresh-token row.
3. Confirm the session is active and unexpired.
4. Confirm the refresh token is active.
5. Confirm the supplied hash matches.
6. Mark the old token as rotated.
7. Insert the replacement token.
8. Update session activity.
9. Commit.

`SELECT ... FOR UPDATE` must be used to prevent two concurrent refresh
requests from both succeeding.

## Next integration step

Implement `PostgresSessionRepository` against the user-management
service's existing PostgreSQL client and migration runner.
