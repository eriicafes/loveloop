import tailwindcss from "@tailwindcss/vite";
import { defineConfig, type Plugin } from "vite";

export default defineConfig({
  plugins: [goDevRefresh(), tailwindcss()],
  server: {
    // The Go server renders the pages on 8100, cmd/dev's proxy or
    // cmd/local -dev, and loads assets from here in development.
    cors: { origin: "http://localhost:8100" },
  },
  build: {
    // web embeds the build, so it goes beside the web package.
    outDir: "web/dist",
    manifest: true,
    rollupOptions: { input: ["web/src/main.css"] },
  },
});

// goDevRefresh lets go-dev reload the browser after it restarts the server.
function goDevRefresh(): Plugin {
  return {
    name: "go-dev-refresh",
    configureServer(server) {
      server.middlewares.use("/__go-dev/refresh", (request, response, next) => {
        if (request.method !== "POST") return next();
        server.ws.send({ type: "full-reload" });
        response.statusCode = 204;
        response.end();
      });
    },
  };
}
