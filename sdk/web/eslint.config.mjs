import eslint from "@eslint/js";
import globals from "globals";
import tseslint from "typescript-eslint";

export default tseslint.config(
  { ignores: ["dist", "node_modules", "test-results"] },
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  { files: ["**/*.ts"], languageOptions: { globals: globals.browser } },
  { files: ["**/*.config.ts", "tests/**/*.{ts,mjs}", "e2e/**/*.spec.ts"], languageOptions: { globals: { ...globals.browser, ...globals.node } } },
);
