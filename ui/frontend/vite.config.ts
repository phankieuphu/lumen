import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vitest/config";

/**
 * keepPlaceholder re-emits the committed .gitkeep that emptyOutDir removes,
 * so a build leaves the git tree clean.
 */
const keepPlaceholder = (): Plugin => ({
  name: "keep-placeholder",
  generateBundle() {
    this.emitFile({ type: "asset", fileName: ".gitkeep", source: "" });
  },
});

export default defineConfig({
  plugins: [react(), keepPlaceholder()],
  build: {
    target: "es2022",
    // The shell embeds this directory (ui/shell/shell.go).
    outDir: "../shell/dist",
    emptyOutDir: true,
    // The shell embeds the build; keep CodeMirror in its own chunk so app code
    // changes don't invalidate the editor bundle.
    rollupOptions: {
      output: {
        manualChunks: (id) => (id.includes("@codemirror") || id.includes("@lezer") ? "editor" : undefined),
      },
    },
  },
  test: { environment: "node", include: ["src/**/*.test.ts"] },
});
