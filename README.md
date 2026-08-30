# Paperless Scanner

Paperless is a local-first document intake service for a network scanner and an always-on Mac. It watches an inbound folder, keeps the raw scan, cleans and straightens pages, runs Tesseract OCR, creates a searchable PDF, and uses a local Ollama model to suggest a filename and archive destination. Filing remains conservative until enough reviewed examples have been learned.

## Quick Start

The Makefile is the main entry point for installation, local use, testing, and the macOS service. See every available command with:

```bash
make help
```

For a first-time setup using the default folders:

```bash
make setup
make run
```

Then open [http://127.0.0.1:8844](http://127.0.0.1:8844), or run `make open`.

`make setup` compiles Paperless, writes the config if it does not exist, installs the required Homebrew tools, downloads the Ollama model, creates the SQLite database, and creates every runtime folder. This includes the configured scanner inbox.

On current macOS Dropbox installations, first-time configuration detects `~/Library/CloudStorage/Dropbox/Dokumente` (or `Documents`) automatically when it exists.

To choose the important folders during first setup:

```bash
make setup \
  INBOX="$HOME/Paperless-Inbox" \
  ARCHIVE="$HOME/Dropbox/Documents"
```

The default config is `~/.paperless/config.toml`. To use a separate local test environment, pass `CONFIG` and `BASE`:

```bash
make setup \
  CONFIG=/tmp/paperless-demo/config.toml \
  BASE=/tmp/paperless-demo/runtime \
  ARCHIVE=/tmp/paperless-demo/archive

make run CONFIG=/tmp/paperless-demo/config.toml
```

## Scanner Inbox

The inbound directory is the folder Paperless watches for new scans. It is configured in TOML:

```toml
[paths]
inbox = "/Users/you/Paperless-Inbox"
```

`paperless init` and `make setup` create this directory automatically. To change an existing configuration, run:

```bash
make configure FORCE=1 \
  INBOX="$HOME/Paperless-Inbox" \
  ARCHIVE="$HOME/Dropbox/Documents"
make init
```

`FORCE=1` rewrites the config, so include every non-default path you want to preserve.

For a one-off watched folder without changing the config:

```bash
make run INBOX="$HOME/Desktop/test-inbox"
```

## Printer Sharing

Paperless checks whether the configured inbox is currently published as an SMB share on macOS. Until it is shared, both `paperless init` and the dashboard show the exact folder and setup steps.

To make it reachable by a Brother scanner:

1. Open System Settings > General > Sharing.
2. Turn on File Sharing.
3. Add the configured Paperless inbox as a shared folder.
4. Open Options and enable "Share files and folders using SMB".
5. Enable the macOS user the scanner will use, then configure the scanner with this Mac's hostname or IP, that username, and the inbox share name.

The Mac and scanner must be reachable on the same network. Use a dedicated macOS account with access limited to the inbox if the scanner supports authenticated SMB credentials.

## Local Model

Paperless defaults to `qwen3.5:9b-q4_K_M`. This 6.6 GB model is the practical choice for a 16 GB Apple silicon Mac: it leaves memory for macOS, OCR image processing, the dashboard, and SQLite while retaining thinking and structured-output support. The model tag is exact so a larger installed Qwen flavor cannot be selected accidentally.

```bash
make model
```

Thinking and Ollama structured output are enabled for document classification. Paperless uses a bounded thinking pass followed by a schema-constrained formatting pass, so reasoning remains visible without consuming the final JSON response. Context is capped at 16K tokens, each pass at 2K generated tokens, and the model is unloaded after every document. The total timeout is six minutes for a 16 GB machine.

The remaining runtime dependencies are Tesseract, German Tesseract language data, Poppler, qpdf, and Ollama. `make setup` or `make init` installs missing dependencies on macOS without triggering a general Homebrew auto-update.

For an isolated environment where those tools are already present, `make init-folders CONFIG=/path/to/config.toml` creates only the folders and database.

## Daily Use

Run the inbox watcher and dashboard together:

```bash
make run
```

Process the inbox once and exit:

```bash
make process
```

Upload a document in the dashboard to send it through the same pipeline as the scanner inbox. The upload receives a normal SQLite job, keeps its raw copy, appears in Recent Scans and All Documents, and is always placed in the Review Queue before filing. The review screen lets you select an existing archive folder or type a new relative folder, correct the document type, adjust the filename, inspect OCR PDF/overlay/text, and approve the final destination.

PDFs downloaded from email or a customer portal use their existing embedded text layer when it is complete. Paperless keeps such PDFs unchanged and skips image cleanup and Tesseract; scanned and mixed-content PDFs still use the OCR pipeline. The review screen labels the preserved file as `Original PDF` and only offers the OCR overlay when OCR was actually run.

Retail receipts are detected from both their narrow scan geometry and point-of-sale text. Scanner-bed margins are cropped before OCR, while normal A4 letters keep their page shape. Receipts default to `Belege`, use `YYYY-MM-DD__merchant__receipt.pdf`, and remain in review until the normal learning policy allows automatic filing.

Analyze an existing document from the terminal without creating a document job:

```bash
make dry-run FILE="$HOME/Downloads/example.pdf"
```

The terminal `dry-run` command remains intentionally isolated. Use dashboard upload when the document should become part of the archive and learning history.

## Archive Folders

`paths.archive_root` is the only root used for final documents. Paperless sends directories discovered below that root together with explicitly configured taxonomy folders to the local model. A configured folder such as `Belege` can therefore be selected before its first approval; approving it creates the directory below the archive root. Sender mappings whose destinations are neither discovered nor configured are ignored.

The active archive root and its discovered folders are visible under Setup in the dashboard. Set the root in `~/.paperless/config.toml` or during configuration:

```bash
make configure FORCE=1 ARCHIVE="$HOME/Library/CloudStorage/Dropbox/Archiv"
make init
```

Approving a typed relative folder creates it below the archive root and records that sender, recipient, document type, and folder as a learned routing example. Absolute paths and paths escaping the archive root are rejected.

## macOS Service

Install and start Paperless as a user LaunchAgent:

```bash
make service-install
make service-start
make service-status
```

A LaunchAgent runs in the logged-in user session, which fits Dropbox, Ollama, Finder-visible files, and home-folder permissions better than a root LaunchDaemon.

## Homebrew Releases

GitHub releases automatically build native macOS binaries for Apple silicon and Intel Macs and update the public `homebrew` repository. The formula selects the matching binary from a checksummed release bundle, so users do not need Go, Bun, or repository credentials.

To publish a version, create a GitHub release whose tag follows semantic versioning, for example `v0.1.0`. The release workflow uploads both macOS archives, updates `Formula/paperless.rb` in the tap, and pushes a matching `paperless-v0.1.0` tap tag. The tap then publishes its own GitHub release. Prereleases receive archives but do not replace the stable Homebrew formula.

Install Paperless directly from the tap:

```bash
brew tap coryoso/homebrew https://github.com/coryoso/homebrew.git
brew install coryoso/homebrew/paperless
```

For the first setup, make sure Ollama is running, initialize the OCR tools and local model, and start Paperless:

```bash
brew services start ollama # Skip this if the Ollama app is already running.
paperless configure
paperless init
brew services start coryoso/homebrew/paperless
```

Paperless starts immediately, restarts if it crashes, and launches again when the macOS user logs in. The dashboard is available at [http://127.0.0.1:8844](http://127.0.0.1:8844).

Upgrade after publishing another release with:

```bash
brew update
brew upgrade coryoso/homebrew/paperless
brew services restart coryoso/homebrew/paperless
```

The app repository stores only the deploy key's private half as the encrypted Actions secret `HOMEBREW_TAP_DEPLOY_KEY`. Its public half is a write-enabled deploy key scoped only to the tap repository.

## Data

The primary SQLite database is local:

```text
~/Library/Application Support/Paperless/paperless.sqlite
```

The database is deliberately not placed directly in Dropbox because sync conflicts can corrupt a live SQLite database. Final PDFs are written below the configured Dropbox archive root.

Routing examples include sender, recipient/addressee, document type, and folder. This allows otherwise similar documents to be learned differently depending on whether they are addressed to a person, household, or company.

## Development

Common project checks are available directly from the Makefile:

```bash
make test-unit
make test
make web-test
make acceptance FILE=/path/to/a/real-scan.pdf
make check
```

The dashboard is a client-side React application created and built with Bun. `bun build` bundles its HTML, TypeScript, React, and CSS into static assets that are embedded into the Go binary. Bun is required to build Paperless but is not required on the Mac running the compiled binary.

For frontend development, run the Go API and Bun development server separately:

```bash
make serve
make web-dev
```

SQL migrations live in `internal/db/migrations/`, typed queries in `internal/db/queries/`, and generated sqlc code in `internal/db/sqlc/`. After changing SQL, run:

```bash
make sqlc
make test
```
