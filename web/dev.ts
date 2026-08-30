import dashboard from "./index.html";

function proxy(request: Request) {
  const source = new URL(request.url);
  const target = new URL(source.pathname + source.search, "http://127.0.0.1:8844");
  return fetch(new Request(target, request));
}

Bun.serve({
  hostname: "127.0.0.1",
  port: 5173,
  development: true,
  routes: {
    "/api/*": proxy,
    "/files/*": proxy,
    "/*": dashboard,
  },
});
