/** @type {import("jest").Config} */
module.exports = {
  rootDir: __dirname,
  testEnvironment: "node",

  roots: [
    "<rootDir>/.jest-build"
  ],

  testMatch: [
    "**/*.test.js"
  ],

  setupFiles: [
    "<rootDir>/jest.env.cjs"
  ],

  moduleFileExtensions: [
    "js",
    "json",
    "node"
  ],

  clearMocks: true,
  restoreMocks: true
};