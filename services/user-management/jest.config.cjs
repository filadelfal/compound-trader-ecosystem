const path = require("node:path");

const projectRoot = __dirname;
const nodeModules = path.resolve(projectRoot, "node_modules");

/** @type {import("jest").Config} */
module.exports = {
  rootDir: projectRoot,
  testEnvironment: "node",
  roots: [
    "<rootDir>/src",
    "<rootDir>/tests",
  ],

  testMatch: [
    "**/*.test.ts",
  ],

  transform: {
    "^.+\\.tsx?$": [
      require.resolve("ts-jest"),
      {
        tsconfig: path.resolve(projectRoot, "tsconfig.jest.json"),
        diagnostics: true,
      },
    ],
  },

  moduleNameMapper: {

    "^\\.\\./src/app$": "<rootDir>/src/app.ts",

    "^\\.\\./\\.\\./src/auth/password/password\\.service$": "<rootDir>/src/auth/password/password.service.ts",

    "^\\.\\./\\.\\./src/auth/session/session\\.service$": "<rootDir>/src/auth/session/session.service.ts",

    "^@auth/password$": path.resolve(
      projectRoot,
      "src/auth/password/index.ts",
    ),

    "^supertest$": require.resolve("supertest"),
  },

  moduleDirectories: [
    nodeModules,
    "node_modules",
  ],

  moduleFileExtensions: [
    "ts",
    "tsx",
    "js",
    "jsx",
    "json",
    "node",
  ],

  clearMocks: true,
  restoreMocks: true,

  collectCoverageFrom: [
    "src/**/*.ts",
    "!src/main.ts",
  ],
};









