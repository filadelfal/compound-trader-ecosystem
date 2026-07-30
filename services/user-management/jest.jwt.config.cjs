const path = require("node:path");

/** @type {import("jest").Config} */
module.exports = {
  rootDir: __dirname,
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
        tsconfig: path.resolve(__dirname, "tsconfig.jest.json"),
        diagnostics: true,
      },
    ],
  },

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
};
