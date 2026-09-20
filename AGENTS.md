# Development commands

Use the root Makefile as the development entry point. Go and Bun must be installed.

- Run `make fmt` after editing Go or frontend files. It applies gofmt and Biome formatting.
- Run `make fmt-check` and `make lint` before finishing changes. These check formatting and run Biome linting and Go vet without rewriting source files.
- Run `make check` for the full validation suite, including formatting, linting, frontend tests, and Go tests. CI and release workflows use `make fmt-check lint web-test test-unit` to skip local OCR acceptance tests.
- For frontend-only work, use `make web-fmt`, `make web-fmt-check`, `make web-lint`, and `make web-test`. Run `make web-build` to check TypeScript and build the embedded assets.

# Frontend and generated assets

Edit frontend source in `web/`. Biome configuration is in `web/biome.json`; it uses the recommended lint rules except CSS descending specificity, because the existing stylesheet intentionally groups component and responsive overrides. Keep the Biome version pinned and commit dependency changes together with `web/bun.lock`.

The Makefile installs dependencies with the frozen Bun lockfile and generates web assets before Go builds, tests, and vet checks. CI and releases rebuild the frontend from source before compiling the Go binary. When invoking Go commands directly in a fresh checkout, run `make web-build` first so the `go:embed` inputs exist.
