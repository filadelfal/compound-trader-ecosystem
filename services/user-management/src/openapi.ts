export const openApiDocument = {
  openapi: "3.1.0",
  info: {
    title: "Compound Trader User Management API",
    version: "1.0.0",
    description: "Authentication, session, authorization, and user-profile API.",
  },
  servers: [{ url: "/api/v1" }],
  tags: [
    { name: "Authentication" },
    { name: "Users" },
    { name: "Operations" },
  ],
  components: {
    securitySchemes: {
      bearerAuth: { type: "http", scheme: "bearer", bearerFormat: "JWT" },
    },
    schemas: {
      Error: {
        type: "object",
        required: ["error"],
        properties: {
          error: {
            type: "object",
            required: ["code"],
            properties: {
              code: { type: "string" },
              requestId: { type: "string" },
              details: {},
            },
          },
        },
      },
    },
  },
  paths: {
    "/auth/register": { post: { tags: ["Authentication"], summary: "Register a user", responses: { "201": { description: "Registered" }, "400": { description: "Invalid input" }, "409": { description: "Email already registered" } } } },
    "/auth/verify-email": { post: { tags: ["Authentication"], summary: "Verify an email address", responses: { "200": { description: "Verified" }, "400": { description: "Invalid or expired token" } } } },
    "/auth/resend-verification": { post: { tags: ["Authentication"], summary: "Request another verification email", responses: { "202": { description: "Request accepted" } } } },
    "/auth/login": { post: { tags: ["Authentication"], summary: "Create an authenticated session", responses: { "200": { description: "Authenticated" }, "401": { description: "Invalid credentials" }, "423": { description: "Account locked" }, "429": { description: "Rate limited" } } } },
    "/auth/refresh": { post: { tags: ["Authentication"], summary: "Rotate a refresh token", responses: { "200": { description: "Tokens rotated" }, "401": { description: "Invalid refresh token" } } } },
    "/auth/logout": { post: { tags: ["Authentication"], summary: "Revoke one refresh session", responses: { "204": { description: "Logged out" } } } },
    "/auth/logout-all": { post: { tags: ["Authentication"], summary: "Revoke all refresh sessions", security: [{ bearerAuth: [] }], responses: { "204": { description: "All sessions revoked" }, "401": { description: "Unauthorized" } } } },
    "/auth/forgot-password": { post: { tags: ["Authentication"], summary: "Request a password reset", responses: { "202": { description: "Request accepted" }, "429": { description: "Rate limited" } } } },
    "/auth/reset-password": { post: { tags: ["Authentication"], summary: "Reset a password", responses: { "200": { description: "Password reset" }, "400": { description: "Invalid input or token" } } } },
    "/users/me": {
      get: { tags: ["Users"], summary: "Get the current user", security: [{ bearerAuth: [] }], responses: { "200": { description: "Current user" }, "401": { description: "Unauthorized" } } },
      patch: { tags: ["Users"], summary: "Update the current user", security: [{ bearerAuth: [] }], responses: { "200": { description: "User updated" }, "400": { description: "Invalid input" }, "401": { description: "Unauthorized" } } },
    },
  },
} as const;
