// Sets the plugin's version in manifest.json, package.json and package-lock.json,
// copies manifest.json to the repository root, and records the version in the root
// versions.json (plugin version -> minimum Obsidian version). Obsidian's community
// directory reads both root files from the default branch.
//   node version-bump.mjs 0.3.0
import { readFileSync, writeFileSync } from "node:fs";

const version = process.argv[2]?.replace(/^v/, "");
if (!version || !/^\d+\.\d+\.\d+$/.test(version)) {
  console.error("usage: node version-bump.mjs X.Y.Z");
  process.exit(2);
}

const read = (f) => JSON.parse(readFileSync(f, "utf8"));
const write = (f, v) => writeFileSync(f, JSON.stringify(v, null, 2) + "\n");

const manifest = read("manifest.json");
manifest.version = version;
write("manifest.json", manifest);
write("../manifest.json", manifest);

const pkg = read("package.json");
pkg.version = version;
write("package.json", pkg);

const lock = read("package-lock.json");
lock.version = version;
if (lock.packages?.[""]) lock.packages[""].version = version;
write("package-lock.json", lock);

const versions = read("../versions.json");
versions[version] = manifest.minAppVersion;
write("../versions.json", versions);

console.log(`okvault plugin ${version} (Obsidian ${manifest.minAppVersion}+)`);
