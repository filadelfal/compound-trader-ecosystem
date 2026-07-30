const path = require("node:path");

const projectRoot = __dirname;
/** @type {import("jest").Config} */
module.exports = {
  rootDir: projectRoot,
  testEnvironment: "node",
  setupFiles: [
    "<rootDir>/jest.env.cjs",
  ],
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







