import { defineConfig } from "vite";

export default defineConfig({ root: "e2e", build: { outDir: "../dist-harness", emptyOutDir: true, rollupOptions: { input: new URL("./e2e/harness.html", import.meta.url).pathname } } });
