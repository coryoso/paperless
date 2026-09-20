# Paperless

Paperless is a local-first document archive for macOS. It accepts scans and uploads, keeps the original document, cleans and straightens scanned pages, runs Tesseract OCR, creates a searchable PDF, and uses Ollama, Bonsai, or Apple Foundation Models to suggest a filename and archive destination. Documents remain on the Mac or in a folder that is locally available through Finder.

## Install with Homebrew

Homebrew is the recommended installation method. It installs the signed, notarized Paperless binary and the OCR/PDF tools without requiring Go, Bun, or access to the source repository.

```bash
brew tap coryoso/homebrew https://github.com/coryoso/homebrew.git
brew install coryoso/homebrew/paperless
```

Paperless defaults to Ollama for local document classification. Skip the installation command if the Ollama app is already installed and running, or if you plan to select Bonsai or Apple Foundation Models in Setup.

```bash
brew install ollama
brew services start ollama
```

Start Paperless now and automatically whenever you log in:

```bash
brew services start coryoso/homebrew/paperless
```

Print and open the configured dashboard address:

```bash
paperless url
open "$(paperless url)"
```

The default is [http://127.0.0.1:8844](http://127.0.0.1:8844). On first launch, Paperless opens an in-app setup guide. The first step asks you to choose the base documents directory. This can be a normal local folder, a mounted SMB share, or a locally synced Dropbox, Google Drive, iCloud Drive, OneDrive, or similar folder. No provider or personal path is built into Paperless.

The guide walks through four steps:

1. **Documents:** choose your archive folder.
2. **Scanner:** follow the macOS SMB sharing instructions, or skip this step and upload files instead.
3. **Model:** choose Ollama, install Bonsai, use Apple Foundation Models, or continue with local rules and add AI later.
4. **Ready:** review your choices and open your archive.

Progress is saved between steps and resumes after a refresh or restart. Inbox processing and uploads stay paused until you finish the guide. Setup reloads work with both `paperless run` / `paperless serve` and background services. Existing configurations with a documents folder keep opening the archive directly; settings remain available from **Setup**.

Upgrade and restart the service after a new stable release with:

```bash
brew update
brew upgrade coryoso/homebrew/paperless
brew services restart coryoso/homebrew/paperless
```

## Usage

### Add and review documents

Drop a PDF, PNG, or JPEG into the scanner inbox, or upload it in the dashboard. Paperless preserves the raw input and sends the document through OCR, classification, and review.

The review screen lets you:

- inspect the original or searchable PDF, OCR overlay, formatted text, Markdown, and raw text;
- correct the recipient and their personal or business capacity;
- choose or create a relative archive folder;
- adjust the document type and filename;
- approve filing or reject and permanently delete the document.

Email and portal PDFs keep their existing text layer when it is complete. Scanned and mixed-content PDFs use the OCR pipeline. Receipts are detected from their scan geometry and point-of-sale text and receive receipt-oriented filing suggestions.

Filing is intentionally conservative. Approved decisions become local routing examples, but conflicting destinations, unknown recipients, and ambiguous business capacities continue to require review. Moving a file directly in Finder does not teach Paperless.

Mahnungen and Zahlungserinnerungen use the **Payment reminder** document type. Recipient detection considers both the addressee and the document's business context: multiple names can represent a private household or business partners, including a GbR without an explicit legal suffix. Joint business obligations use business filing areas; personal correspondence stays separate. Inferred partnerships and shared personal/business names require confirmation when the identity remains ambiguous.

In **Setup → Recipients & learning**, add **Shared postal addresses** for locations receiving mail for several people, or edit a recipient to save addresses for that person/business. Enter the street and house number followed by the postcode and city, with a blank line between addresses. Detection prioritizes names immediately above matching postal address blocks and recognizes common spellings such as `Straße` and `Str.`. Address associations currently support numeric 4–5 digit postcodes. A shared address never chooses a person by itself; conflicting identities or personal/business evidence still require review. The detected address is shown with the document, and saved address changes apply to subsequent classifications without restarting.

### Connect a network scanner

The default inbox is `~/Paperless/inbox`. Paperless checks whether it is published as an SMB share and shows the configured folder in Setup.

1. Open **System Settings → General → Sharing**.
2. Turn on **File Sharing**.
3. Add the Paperless inbox as a shared folder.
4. Open **Options** and enable **Share files and folders using SMB**.
5. Enable the macOS account the scanner should use.
6. Configure the scanner with the Mac's hostname or IP address, that account, and the inbox share name.

The Mac and scanner must be reachable on the same network. A dedicated macOS account with access limited to the inbox is preferable when the scanner supports authenticated SMB credentials.

### Storage and backups

The live SQLite database stays outside the selected documents directory:

```text
~/Library/Application Support/Paperless/paperless.sqlite
```

Keeping the live database local prevents cloud-sync conflicts from corrupting SQLite or its write-ahead state. Completed PDFs are filed below the user-selected documents directory.

Paperless creates a transactionally consistent database snapshot shortly after startup and every 24 hours. It validates each snapshot, copies it under a temporary name, and atomically renames it into `<documents directory>/.paperless-backups`. The newest 14 backups are retained, so sync clients see only complete database files. Use **Back up now** in Setup or run:

```bash
paperless backup
```

### Configuration and dashboard port

The private user configuration is `~/.paperless/config.toml` in the home directory of the user running Paperless (unless overridden with `paperless --config /path/to/config.toml …`). On a fresh installation, Paperless uses built-in defaults without creating this file. The setup guide creates it when you choose the documents folder; `paperless configure` also creates it. The `.paperless` folder is hidden in Finder; use **Go → Go to Folder** and enter `~/.paperless`. The dashboard binds to loopback port `8844` by default, but its URL is derived from the configured host and port rather than being fixed in the application. `paperless url` always prints the address for the current configuration.

Choose a different port during command-line setup with:

```bash
paperless configure --port 9988
```

For an existing installation, change `service.port` in the TOML file and restart Paperless:

```bash
brew services restart coryoso/homebrew/paperless
```

The archive root must be selected by the user. Relative filing destinations are constrained below that root; absolute paths and paths that escape it are rejected.

### Apple Foundation Models

In **Setup → Choose your local model**, select **Apple Foundation Models** and click **Save model**. Paperless checks `fm available --model system`, saves the choice, and restarts. This requires a Mac with Apple Intelligence enabled and an installed `fm` command supporting `respond`, `count-tokens`, and structured schemas. Check availability in Terminal first:

```bash
fm available --model system
```

For a new command-line setup, use `paperless configure --llm-provider fm` or `make setup LLM_PROVIDER=fm`. For an existing configuration, change `provider = "fm"` in the `[llm]` section of `~/.paperless/config.toml` and restart Paperless. The model resolves to `system`, even if an old Qwen tag remains in the file. `paperless doctor` checks the selected provider.

Paperless calls `fm respond` directly, with document text passed through standard input and an enforced output schema. No `fm serve`, Ollama service, or Ollama model download is needed. The existing recipient, folder, filename, and retention policies still apply. Ollama-specific endpoint, context, reasoning, output, and keep-alive settings are ignored; `timeout_seconds` applies to all providers.

Apple's on-device model has a smaller context window than the default Qwen configuration. Paperless counts tokens and shortens long document excerpts or filing context as needed; these documents always require review. A missing command, unavailable model, refusal, timeout, or invalid response falls back to local rules and is reported in processing progress. Select Ollama again in Setup to return to Qwen 3.5; a custom Ollama tag can be set through `llm.model` in the configuration.

### Bonsai (PrismML)

In **Setup → Choose your local model**, select **Bonsai · PrismML**, then **Install & use Bonsai 8B**. Keep the page open while installation runs. Paperless downloads the [Bonsai 8B 1-bit model](https://huggingface.co/prism-ml/Bonsai-8B-gguf) (about 1.16 GB) and PrismML's llama.cpp runtime, starts a local server, verifies readiness, and saves the provider. Interrupted model downloads resume when you retry. A failed installation leaves the selected provider unchanged.

For a new command-line setup:

```bash
make setup LLM_PROVIDER=bonsai
# Or, with an installed Paperless binary:
paperless configure --llm-provider bonsai
paperless init
```

Automatic installation requires macOS and the Command Line Tools (`git` and `curl`). It uses a pinned [Bonsai-demo](https://github.com/PrismML-Eng/Bonsai-demo) revision and verified model weights, stored under `<state_dir>/bonsai`. The installer downloads the text inference components; Python, MLX, Open WebUI, and the demo's code interpreter are not needed. The per-user LaunchAgent `com.paperless.bonsai` starts the server at login on `127.0.0.1`. Installation tries the configured port (8080 by default) and automatically chooses a free local port if it is occupied. After the server is ready, both browser and command-line installation save the selected address as `bonsai.endpoint` in the active configuration file. The LaunchAgent retains that port across restarts. Installation and server logs are at `<state_dir>/bonsai-install.log` (browser installs) and `<state_dir>/bonsai/server.log`.

To connect an existing Bonsai-demo llama.cpp server, set the following in your config and use **Save model** in Setup. Both a server root URL and a URL ending in `/v1` work. Set `model` to its exact `/v1/models` ID, or launch the upstream server with `--alias Bonsai-8B`. Other Bonsai families, including Bonsai 2, can use their own model ID here. Managed installation always installs Bonsai 8B on a local loopback address.

```toml
[llm]
provider = "bonsai"

[bonsai]
endpoint = "http://127.0.0.1:8080"
model = "Bonsai-8B"
```

Bonsai's configuration is separate from Ollama's `llm.endpoint` and `llm.model`, so switching between them preserves those settings. `llm.timeout_seconds` and `llm.max_output_tokens` control Bonsai requests; `llm.context_tokens` sets the managed server's context when installed. Bonsai uses constrained JSON with thinking disabled. Recipient, folder, filename, and retention policies still apply locally. Shortened document excerpts require review; unavailable servers or invalid results fall back to local rules. `paperless doctor` checks the selected server and model.

For Bonsai 8B on the pinned runtime, launch an existing server with `--no-jinja` so structured JSON works with its non-thinking template. Managed installations include this flag automatically.

The Bonsai service remains installed when switching providers. To stop it and prevent startup at login:

```bash
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.paperless.bonsai.plist"
rm "$HOME/Library/LaunchAgents/com.paperless.bonsai.plist"
```

### Command-line operations

```bash
paperless doctor               # Check tools, model, and configured paths
paperless process-once         # Process the inbox once and exit
paperless dry-run example.pdf  # Analyze a document without creating a job
paperless backup               # Create an immediate SQLite snapshot
paperless url                  # Print the configured dashboard URL
```

Run `paperless help` for the complete command list.

## Development

Building from source requires Go, Bun, the OCR/PDF tools, and Ollama, Bonsai, or an available Apple Foundation Models `fm` command. The Makefile is the main development entry point:

```bash
make help
make setup
make run
```

`make setup` builds Paperless, opens the native macOS folder chooser, writes the private user configuration, installs missing runtime dependencies, downloads the configured Ollama model, installs Bonsai, or checks `fm` for Apple Foundation Models, creates the SQLite database, and prepares the runtime folders.

Use a disposable configuration for development or testing:

```bash
make setup \
  CONFIG=/tmp/paperless-demo/config.toml \
  BASE=/tmp/paperless-demo/runtime \
  ARCHIVE=/tmp/paperless-demo/archive \
  PORT=9988

make run CONFIG=/tmp/paperless-demo/config.toml
make open CONFIG=/tmp/paperless-demo/config.toml
```

Common checks:

```bash
make fmt
make fmt-check
make lint
make test-unit
make test
make web-test
make acceptance FILE=/path/to/a/real-scan.pdf
make check
```

`make fmt` formats Go with gofmt and the frontend with Biome. `make fmt-check` and `make lint` are read-only checks; both run in CI and before release builds. Frontend-only commands are `make web-fmt`, `make web-fmt-check`, and `make web-lint`. `make check` runs formatting checks, linting, frontend tests, and the full Go test suite.

To check real inference against a running Bonsai 8B server using a synthetic invoice:

```bash
PAPERLESS_BONSAI_TEST_ENDPOINT=http://127.0.0.1:8080 go test -count=1 ./internal/classify -run '^TestBonsaiLiveClassification$'
```

The React dashboard source lives in `web/` and is built with Bun and embedded into the Go binary. Generated files in `internal/app/webdist/` are ignored by Git and rebuilt in CI and for every release. The Makefile installs frontend dependencies from the lockfile and builds these assets before Go builds, tests, and vet checks. If invoking Go directly from a fresh checkout, run `make web-build` first.

For frontend development, run the API and Bun development server separately:

```bash
make serve
make web-dev
```

SQL migrations live in `internal/db/migrations/`, queries in `internal/db/queries/`, and generated sqlc code in `internal/db/sqlc/`. After changing SQL, run `make sqlc` and `make test`.

The OCR pipeline retains the raw text and searchable PDF separately from reconstructed Markdown. Layout reconstruction estimates headings, paragraphs, columns, and table cells from Tesseract word positions without rewriting recognized words. Uploads use two OCR workers by default; configure `ocr.workers` from 1–8 to change concurrency.

## Releases and the Homebrew tap

The Paperless repository is the source of truth for releases. Publishing a stable semantic-versioned GitHub release, such as `v1.2.3`, triggers one workflow that:

1. checks formatting and linting, tests the application, builds the web assets from source, and embeds them in native Apple silicon/Intel binaries;
2. signs the binaries with Developer ID and waits for Apple notarization;
3. verifies the signatures again after packaging and publishes checksums;
4. updates `Formula/paperless.rb` directly in `coryoso/homebrew`.

Prereleases receive downloadable artifacts but do not update Homebrew. The tap contains only formula definitions and does not create duplicate releases of its own. Signing setup and rotation are documented in [docs/CODE_SIGNING.md](docs/CODE_SIGNING.md).
