# Paperless Scanner

Paperless is a local-first document intake service for a network scanner and an always-on Mac. It watches an inbound folder, keeps the raw scan, cleans and straightens pages, runs Tesseract OCR, creates a searchable PDF, and uses a local Ollama model to suggest a filename and archive destination. Filing remains conservative until enough reviewed examples have been learned.

## Quick Start

The Makefile is the main entry point for installation, local use, testing, and the macOS service. See every available command with:

```bash
make help
```

For a first-time setup:

```bash
make setup
make run
```

Then open [http://127.0.0.1:8844](http://127.0.0.1:8844), or run `make open`.

`make setup` opens the native macOS folder chooser because Paperless requires the user to select a base documents directory. Choose any existing folder that is locally accessible through Finder: an ordinary folder, a mounted file server, or a folder synced by Dropbox, Google Drive, iCloud Drive, OneDrive, or another provider. Paperless does not assume a provider or include a personal path in its defaults.

The setup then writes the private user config, installs the required Homebrew tools, downloads the Ollama model, creates the local SQLite database, and creates every runtime folder. This includes the dedicated scanner inbox. The same guided storage and SMB instructions remain available under **Setup** in the dashboard.

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

## Scanner Sharing

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

Upload a document in the dashboard to send it through the same pipeline as the scanner inbox. The upload receives a normal SQLite job, keeps its raw copy, appears in Recent Scans and All Documents, and is always placed in the Review Queue before filing. The review screen lets you navigate each level of the archive folder path, use the separate **Other directory…** button for a new relative folder, correct the recipient and their personal/business capacity, select the document type, and adjust the filename. Paper-original retention is a highlighted recommendation derived from the final document type and configured policy; no extra selection is required. Inspect the PDF/overlay/text before approving the final destination. Processing, Review, Documents, and Setup keep navigation fixed and scroll their content within the viewport. Rejecting a review permanently deletes its database record, raw copy, review copy, and OCR workspace.

PDFs downloaded from email or a customer portal use their existing embedded text layer when it is complete. Paperless keeps such PDFs unchanged and skips image cleanup and Tesseract; scanned and mixed-content PDFs still use the OCR pipeline. The review screen labels the preserved file as `Original PDF` and only offers the OCR overlay when OCR was actually run.

Retail receipts are detected from both their narrow scan geometry and point-of-sale text. Scanner-bed margins are cropped before OCR, while normal A4 letters keep their page shape. Letters align to text baselines before considering paper edges, so skewed scanner-bed edges do not override straight writing. Receipts default to `Belege`, use `YYYY-MM-DD__merchant__receipt.pdf`, and remain in review until the normal learning policy allows automatic filing.

Analyze an existing document from the terminal without creating a document job:

```bash
make dry-run FILE="$HOME/Downloads/example.pdf"
```

The terminal `dry-run` command remains intentionally isolated. Use dashboard upload when the document should become part of the archive and learning history.

## Archive Folders

`paths.archive_root` is the user-selected base documents directory and the only root used for final documents. Paperless sends directories discovered below that root together with explicitly configured taxonomy folders to the local model. A configured folder such as `Belege` can therefore be selected before its first approval; approving it creates the directory below the archive root. Sender mappings whose destinations are neither discovered nor configured are ignored.

The selected directory and its discovered folders are visible under Setup in the dashboard. Use **Choose documents folder** there, run `paperless configure` for the native picker, or provide an explicit local path for unattended configuration:

```bash
make configure FORCE=1 ARCHIVE="$HOME/Documents"
make init
```

Approving a typed relative folder creates it below the archive root and records that sender, recipient, personal/business capacity, document type, and folder as a learned routing example. Absolute paths and paths escaping the archive root are rejected.

## macOS Service

Install and start Paperless as a user LaunchAgent:

```bash
make service-install
make service-start
make service-status
```

A LaunchAgent runs in the logged-in user session, which fits Dropbox, Ollama, Finder-visible files, and home-folder permissions better than a root LaunchDaemon.

## Homebrew Releases

GitHub releases automatically build native macOS binaries for Apple silicon and Intel Macs and update the public `homebrew` repository. The formula selects the matching binary from a checksummed release bundle, so users do not need Go, Bun, or repository credentials. When the Apple credentials described in [Code signing](docs/CODE_SIGNING.md) are configured, the macOS runner signs every distributed binary with a Developer ID certificate and submits it to Apple's notarization service before calculating those checksums.

To publish a version, create a GitHub release whose tag follows semantic versioning, for example `v0.1.0`. The release workflow uploads both macOS archives, updates `Formula/paperless.rb` in the tap, and pushes a matching `paperless-v0.1.0` tap tag. The tap then publishes its own GitHub release. Prereleases receive archives but do not replace the stable Homebrew formula.

Install Paperless directly from the tap:

```bash
brew tap coryoso/homebrew https://github.com/coryoso/homebrew.git
brew install coryoso/homebrew/paperless
```

For the first setup, start your existing Ollama app or install its Homebrew service, then start Paperless:

```bash
# Skip these two lines if the Ollama app is already running.
brew install ollama
brew services start ollama

brew services start coryoso/homebrew/paperless
```

Open [http://127.0.0.1:8844](http://127.0.0.1:8844), choose the base documents directory, and follow the displayed SMB sharing steps. The service safely waits for that selection before processing documents, restarts after saving it, restarts if it crashes, and launches again when the macOS user logs in.

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

The database is deliberately not placed directly in the selected documents directory because sync conflicts can corrupt a live SQLite database. Final PDFs are written below that directory instead.

Paperless creates a transactionally consistent snapshot 30 seconds after the configured service starts and every 24 hours thereafter. Snapshots are validated locally, copied under a temporary name, and atomically renamed inside `<documents directory>/.paperless-backups`; the newest 14 are retained. A Dropbox, Google Drive, or other sync client therefore sees only completed backup files rather than the live database or its write-ahead state. Use **Back up now** under Setup or run `paperless backup` for an immediate snapshot.

Routing examples include sender, recipient/addressee, capacity (personal, sole proprietor, GbR, other organization, or unknown), document type, folder, and filename. They are persisted in `paperless.sqlite` inside the configured state directory. Setup displays the database location and approval count. The model receives up to 12 relevant approved routing patterns from the 500 most recently recorded distinct patterns; matching learned destinations are included even when the normal folder shortlist would omit them. Exact sender/recipient/capacity/type matches also guide local rule suggestions. Conflicting destinations and ambiguous recipients remain subject to review. This is retrieval of saved examples, not model retraining. Moving files directly in Finder does not create learning examples. Earlier examples without a capacity are retained as unknown and do not count toward capacity-specific automatic filing.

**Setup → Recipients & learning** lets you add and edit recipient names, aliases, capacities, and optional filing areas. Review offers saved recipients first and a separate **New recipient…** choice. Confirmed new recipients are added automatically on approval. Selecting a saved recipient resolves to its stored name and capacity; when the detected name differs, successful approval adds it as an alias. The review shows the alias that will be learned, and aliases remain editable in Setup. Known aliases are reused on later documents, including confirmed OCR misspellings. Conflicting aliases do not automatically pick an identity. The same name can have separate personal and sole proprietor profiles. A GbR is separate from its individual partners: a personally addressed tax reminder cannot route into a GbR area merely because the person's name appears in the company name. Filing areas reserve a directory subtree for a recipient/capacity, and the most specific configured area wins. Ambiguous business capacity or inconsistent recipient evidence requires review.

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

### Readable document text

The **Text** preview offers **Formatted**, **Markdown**, and **Raw text**, plus a Markdown download. For scans, headings, paragraphs, columns, and table cells are reconstructed from Tesseract's saved word positions. Existing scans can use this view without being processed again. New processing saves `document.md` in the job workspace; classification uses reconstructed reading order for local rules and Markdown for the local model. Raw OCR, its duplicate-detection hash, and the searchable PDF are retained separately. Embedded PDF text keeps its original fixed spacing when no OCR geometry exists. Layout is an estimate: it does not rewrite recognized words or guarantee correct table boundaries, so the scan and raw text remain available for comparison.

### Parallel upload processing

Uploads use two OCR workers by default, so image cleanup and Tesseract for separate documents can overlap. Classification and final processing run one document at a time, allowing OCR to proceed while the local model is busy. Each page's searchable PDF, plain text, and word positions are generated by one Tesseract recognition pass with one OpenMP thread. Configure `[ocr] workers = 2` in the TOML configuration to adjust parallelism (1–8; unset or nonpositive uses 2). The queue keeps at most one more active document than OCR workers to bound memory and disk work. Progress distinguishes waiting for OCR from waiting for classification.
