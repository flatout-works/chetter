import { sveltekit } from "@sveltejs/kit/vite";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";
import { execSync } from "node:child_process";

function gitHash() {
  try {
    return execSync("git rev-parse --short HEAD", { encoding: "utf8" }).trim();
  } catch {
    return "unknown";
  }
}

const proxyTarget = process.env.VITE_DEV_PROXY_TARGET || "http://localhost:8090";

export default defineConfig({
  plugins: [tailwindcss(), sveltekit()],
  define: {
    __WEB_GIT_HASH__: JSON.stringify(gitHash()),
  },
  server: {
    proxy: {
      "^/api(\\.v1)?": {
        target: proxyTarget,
        changeOrigin: true,
        secure: process.env.VITE_DEV_PROXY_SECURE === "true",
      },
    },
  },
});
