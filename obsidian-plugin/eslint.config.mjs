// Obsidian's own lint rules, the ones the community directory's scorecard checks
// every release against: `npm run lint`.
import { defineConfig, globalIgnores } from "eslint/config";
import obsidianmd from "eslint-plugin-obsidianmd";

export default defineConfig([
  globalIgnores(["node_modules", "main.js", "esbuild.config.mjs", "version-bump.mjs", "eslint.config.mjs", "test"]),
  ...obsidianmd.configs.recommended,
  {
    languageOptions: {
      parserOptions: { projectService: true, tsconfigRootDir: import.meta.dirname },
    },
  },
]);
