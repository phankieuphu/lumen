import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  build: {
    target: "es2022",
    // The shell embeds dist/; keep CodeMirror in its own chunk so app code
    // changes don't invalidate the editor bundle.
    rollupOptions: {
      output: {
        manualChunks: (id) => (id.includes("@codemirror") || id.includes("@lezer") ? "editor" : undefined),
      },
    },
  },
  test: { environment: "node", include: ["src/**/*.test.ts"] },
});
