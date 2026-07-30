const fs = require("node:fs");
const path = require("node:path");

module.exports = (request, options) => {
  const rootDir = options.rootDir || process.cwd();

  if (request.startsWith("src/")) {
    const candidate = path.resolve(rootDir, request);

    const possibleFiles = [
      candidate,
      `${candidate}.ts`,
      `${candidate}.tsx`,
      `${candidate}.js`,
      `${candidate}.cjs`,
      `${candidate}.mjs`,
      path.join(candidate, "index.ts"),
      path.join(candidate, "index.tsx"),
      path.join(candidate, "index.js"),
    ];

    const resolvedFile = possibleFiles.find((file) => fs.existsSync(file));

    if (resolvedFile) {
      return resolvedFile;
    }
  }

  return options.defaultResolver(request, options);
};
