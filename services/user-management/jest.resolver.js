const path = require("node:path");

/**
 * Jest resolver that supports extensionless TypeScript imports
 * and TypeScript directory index files.
 */
module.exports = (request, options) => {
  const candidates = [
    request,
    `${request}.ts`,
    path.join(request, "index.ts"),
  ];

  let lastError;

  for (const candidate of candidates) {
    try {
      return options.defaultResolver(candidate, options);
    } catch (error) {
      lastError = error;
    }
  }

  throw lastError;
};
