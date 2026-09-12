import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";
import { cpSync, mkdirSync } from "node:fs";
export default defineConfig({
  plugins: [react(), tailwindcss(), { name: "local-pdf-assets", closeBundle() {
    const target = fileURLToPath(new URL("./dist/file-viewer/pdfjs/", import.meta.url)); mkdirSync(target, { recursive: true });
    for (const name of ["cmaps", "standard_fonts", "wasm"]) cpSync(fileURLToPath(new URL(`./node_modules/pdfjs-dist/${name}`, import.meta.url)), `${target}/${name}`, { recursive: true });
    cpSync(fileURLToPath(new URL("./node_modules/pdfjs-dist/LICENSE", import.meta.url)), `${target}/LICENSE`);
  } }],
  worker: { format: "es" },
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  build: { outDir: "dist", emptyOutDir: true, rollupOptions: { input: { main: fileURLToPath(new URL("./index.html", import.meta.url)), callback: fileURLToPath(new URL("./oauth-callback.html", import.meta.url)) } } },
});
