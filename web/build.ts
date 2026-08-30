import { rm } from "node:fs/promises";
import { resolve } from "node:path";

const outdir = resolve(import.meta.dir, "../internal/app/webdist");
await rm(outdir, { recursive: true, force: true });

const result = await Bun.build({
  entrypoints: [resolve(import.meta.dir, "index.html")],
  outdir,
  minify: true,
  target: "browser",
});

if (!result.success) {
  for (const log of result.logs) console.error(log);
  process.exitCode = 1;
}
