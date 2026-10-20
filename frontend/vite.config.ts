import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";
export default defineConfig({
  plugins: [react(), tailwindcss()],
  worker: { format: "es" },
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  build: { outDir: "dist", emptyOutDir: true, rollupOptions: { input: { main: fileURLToPath(new URL("./index.html", import.meta.url)), callback: fileURLToPath(new URL("./oauth-callback.html", import.meta.url)) } } },
});
