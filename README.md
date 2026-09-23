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

- inspect the original or searchable PDF, OCR overlay, saved JSON blocks, formatted text, and consolidated metadata;
- correct the recipient and their personal or business capacity;
- choose or create a relative archive folder;
- adjust the recipient, folder, and filename;
- approve filing or reject and permanently delete the document.

Email and portal PDFs keep their existing text layer when it is complete. Scanned and mixed-content PDFs use the OCR pipeline.

For multipage scans, pages are individually straightened and cropped, then fitted to a shared page format before OCR. The majority page size determines that format; if crops vary too much, the original scan sizes provide the fallback. When neither has a majority, a shared canvas accommodates every cleaned page. Smaller crops receive white margins, larger pages are scaled proportionally to fit, and portrait/landscape orientation is preserved. Single-page scans keep their individual crop size.

Receipts are detected from their scan geometry and point-of-sale text and receive receipt-oriented filing suggestions.

**Reprocess document** runs OCR and classification again while keeping the previously archived file. When approving the new review, **Replace previous file** saves the reviewed PDF under the chosen name and folder and removes the previous archive copy. Choose **Keep both files** to retain it and save another copy; the document record then points to the new copy. The review shows the previous path and defaults to replacement. Unrelated filename collisions receive a numbered suffix.

Filing is intentionally conservative. Approved decisions become local routing examples, but conflicting destinations, unknown recipients, and ambiguous business capacities continue to require review. Moving a file directly in Finder does not teach Paperless.

Recipient detection considers both the addressee and the document's business context: multiple names can represent a private household or business partners, including a GbR without an explicit legal suffix. Joint business obligations use business filing areas; personal correspondence stays separate. Inferred partnerships and shared personal/business names require confirmation when the identity remains ambiguous.

In **Setup → Recipients & learning**, add **Shared postal addresses** for locations receiving mail for several people, or edit a recipient to save addresses for that person/business. Enter the street and house number followed by the postcode and city, with a blank line between addresses. Detection prioritizes names immediately above matching postal address blocks and recognizes common spellings such as `Straße` and `Str.`. Address associations currently support numeric 4–5 digit postcodes. A shared address never chooses a person by itself; conflicting identities or personal/business evidence still require review. The detected address is shown with the document, and saved address changes apply to subsequent classifications without restarting.

### Structured document JSON

In the document preview, **Overlay → Bounding boxes** shows the saved
proximity grouping of OCR words. The automatic distance is each page's median
word height. The default distance is applied automatically, without viewer controls. Words join when the
Euclidean gap between their rectangles is within that distance, including
connections through neighboring words. Groups never cross pages.

**Text → JSON** shows the same groups with a **Save JSON** download. Each object
contains `id`, `page`, `type`, `representation`, `position: {x, y}`,
`size: {width, height}`, and `content`. Coordinates use source-image pixels from
the top-left corner. Groups follow page order, then top-to-bottom and left-to-right,
retaining line breaks within each group.

Bonsai automatically fills the roles during processing: `text`, `sender`, `recipient`,
`date`, `subject`, `reference`, or `payment`. Each block gets one dominant role;
uncertain or mixed content uses `text`. These roles apply to all kinds of documents,
not just invoices. `representation` separately describes a `paragraph` or `table`.
Repeated aligned cells provide a table hint; multiple text lines alone do not.
Labels appear on the bounding boxes and in the JSON. The model only supplies labels;
the original text and geometry are preserved.
Role and table predictions are experimental. Inspect them against the scan:
long, noisy documents and detached address fragments can be mislabeled, and
proximity can combine fields with different roles.

This uses Bonsai's constrained JSON schema, classifying one block at a time.
The model receives compact JSON with only `id` and `content`; page numbers,
positions, dimensions, old labels and geometry hints stay out of the prompt.
Explicit model labels such as `document_title` map back to the seven public types.
Short fragments (up to 160 characters, without sentence-ending punctuation)
initially labeled `text` get a second pass
with up to three preceding and three following blocks from the same page.
This gives ambiguous fragments nearby context without diluting clear fields with
the entire document. Table candidates additionally request a representation label;
blocks without aligned cells use `paragraph`.

The running server's tokenizer and context size are checked with space reserved
for output. Oversized input is rejected explicitly, never silently truncated.
Requests are limited to 256 blocks and a 1 MiB request body; text capacity is
checked against the actual token window. Any invalid model response rejects the
whole result.
Bonsai 8B supports a 65,536-token context window. To use it, configure
`llm.context_tokens = 65536` and start the model runtime with the same context size
(the managed installer uses this setting when creating its LaunchAgent).

The JSON array is persisted in SQLite with its OCR source revision. Labels and
geometry survive reloads and are included in database backups. The overlay renders
these same saved coordinates. OCR words are clustered at the default distance,
classified, then consolidated into a separately persisted `unified_json` document.
Same-role metadata fragments contained in a more complete occurrence are merged;
conflicting values and distinct body paragraphs remain separate. Every consolidated
entry retains all contributing block IDs. Original blocks remain unchanged.

A constrained Bonsai pass extracts sender/recipient names, postal addresses, phones,
faxes, emails, websites, and the original-language subject from candidate groups.
Values unsupported by source content are dropped; incomplete extraction stays
visible for review. Text offers **Formatted**, **JSON**, and **Fields** views.
Fields shows normalized date, subject, sender and recipient details, plus consolidated references, payment information and source blocks;
original OCR blocks and extracted values remain unchanged. Both JSON representations can be downloaded. The document title uses its subject,
with multiline sender and recipient details shown separately. Proposed filenames use
`date__sender__subject.pdf`, retaining the subject's language and normalizing unsafe
filename characters. Missing subjects use `document`, not a translated summary.

New documents no longer generate or store Markdown. Raw OCR and searchable PDFs
remain source artifacts. Native-text PDFs use paragraph blocks with null position
and size. Existing records without unified metadata can be reprocessed; legacy
Markdown-only records can still be imported once for compatibility. Bonsai is
currently required for automatic block labels and structured metadata extraction.

### Similar previously approved documents

In **Setup → Compare similar documents**, enable local document similarity after installing Ollama and its embedding model:

```bash
brew install ollama
brew services start ollama
ollama pull embeddinggemma
```

If Ollama is already running, only the model download is needed. Save the similarity settings after active uploads finish; Paperless checks the model and restarts. Similarity uses a separate embedding model and works alongside **Apple Foundation Models, Bonsai, Ollama, or local rules** for classification. The first version supports Ollama embeddings on a loopback address only. Selecting FM or Bonsai does not install or start Ollama automatically.

The review panel shows up to five closest previously approved documents, their approved folders and recipients, and the matching block text. These are comparisons, not filing-confidence scores: related documents can have different purposes or recipients. Similarity does not change classification, auto-filing, or your review choices.

Existing approved documents and documents awaiting review are indexed in the background. Persisted unified JSON supplies deduplicated entry text for embeddings; long entries split into overlapping sections without truncation. Older records fall back to deterministic consolidation until reprocessed. JSON syntax, labels and coordinates are not embedded. Each stored chunk retains all contributing source block IDs, whose metadata and coordinates remain in the original JSON. The index is stored in the local SQLite database using sqlite-vec cosine distance and is included in database backups. JSON hashes and installed model digests trigger reindexing when the saved document or model weights change. Saving JSON atomically invalidates previous vectors, and the versioned index rebuilds previous Markdown embeddings. Deleting a document also deletes its embedding records. Missing text and service outages are shown without blocking review; the service retries indexing every 30 seconds. Text exceeding 2 MiB or 256 sections is reported as unavailable for similarity rather than silently truncated.

Under **Embedding model settings**, you can select another installed Ollama embedding model or local server address. Configuration is separate from `[llm]` and `[bonsai]`:

```toml
[embeddings]
enabled = true
endpoint = "http://127.0.0.1:11434"
model = "embeddinggemma"
timeout_seconds = 120
```

Similarity is optional and disabled by default. Disabling it stops indexing and comparisons while retaining the rebuildable index. Search first selects up to 32 approved documents by their mean section vector, then compares passages within those candidates. This bounds passage comparison work as the archive grows, but can omit a matching passage in a document with otherwise different content. Existing caches without document vectors rebuild automatically after upgrading. Retrieval quality should be evaluated on your archive before using similarity to influence filing suggestions.

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

The dashboard receives live change notifications over Server-Sent Events at `/api/dashboard/events`, then fetches a fresh snapshot. The processing pipeline explicitly publishes after saving each document stage, including received, processing, OCR complete, classified, review, archived, duplicate, and failed. Approval, rejection, recipient edits, folder refreshes, and backups also publish after their changes succeed. There are no database triggers, filesystem watchers, or periodic dashboard checks.

Within the service these notifications go directly to connected browsers. Separate CLI processes relay their pipeline notifications to the running service using a private discovery record in the state directory and an authenticated internal HTTP endpoint. This works with the configured host and port and keeps different state directories separate. An unavailable dashboard never fails document processing; reconnecting clients fetch a fresh snapshot.

Each browser tab shares one SSE connection for dashboard notifications and progress from all its uploads, keeping HTTP/1.1 connections available for API requests even during large upload batches. The browser reconnects automatically after interruptions and catches up when a hidden tab becomes visible again. Reconnecting replays retained upload progress without duplicating events; durable job status settles uploads whose in-memory history was lost during a service restart. SSE uses the dashboard's own host and port, so it also works when accessing a reachable service from another machine. Reverse proxies must allow streaming responses without buffering; the service sends idle heartbeats and an `X-Accel-Buffering: no` header.

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

The OCR pipeline retains the raw text and searchable PDF separately from the saved block JSON. Formatted text renders saved blocks directly; Markdown is not generated. Uploads use two OCR workers by default; configure `ocr.workers` from 1–8 to change concurrency.

## Releases and the Homebrew tap

The Paperless repository is the source of truth for releases. Publishing a stable semantic-versioned GitHub release, such as `v1.2.3`, triggers one workflow that:

1. checks formatting and linting, tests the application, builds the web assets from source, and embeds them in native Apple silicon/Intel binaries;
2. signs the binaries with Developer ID and waits for Apple notarization;
3. verifies the signatures again after packaging and publishes checksums;
4. updates `Formula/paperless.rb` directly in `coryoso/homebrew`.

Prereleases receive downloadable artifacts but do not update Homebrew. The tap contains only formula definitions and does not create duplicate releases of its own. Signing setup and rotation are documented in [docs/CODE_SIGNING.md](docs/CODE_SIGNING.md).

GitHub's **Generate release notes** uses [.github/release.yml](.github/release.yml) to group merged pull requests into **Features** (`enhancement` label), **Fixes** (`bug` label), and **Miscellaneous** (everything else, including unlabeled PRs).

The [PR labeling workflow](.github/workflows/pr-labels.yml) automatically syncs these labels from conventional PR titles when a PR is opened, edited, reopened, or closed: `feat: ...` and `feat(scope): ...` get `enhancement`; `fix: ...` and `fix(scope): ...` get `bug`. Breaking-change prefixes such as `feat!: ...` and `fix(scope)!: ...` work too. Other prefixes go under Miscellaneous. The workflow manages both category labels, removing stale ones after title changes while preserving unrelated labels. Wait for it to finish before generating release notes.

Document categories have been removed from extraction schemas, review controls,
SQLite routing examples and learned routing keys. Routing uses sender, recipient,
capacity and destination. Paper retention requires review rather than a category rule.
The normalized metadata projection removes salutations and normalizes shouting-case
names and postal lines while preserving mixed-case spelling and short organization
acronyms. Postal addresses retain ordered lines instead of assuming a country-specific
address schema. Saved recipient profiles override the normalized identity on approval;
the extracted identity and source OCR are preserved. Original upload names are available
in a labeled disclosure; the header shows the current proposed filename.


Postal addresses persist explicit `street_name`, `house_number`, `postal_code` and
`city` components alongside source lines. Unknown components stay empty. The most
complete address is primary (first occurrence breaks ties); alternatives remain
available. The recipient review form accepts separate street/number, postcode and
city inputs, and saves explicit components when approved. Dates use the browser's
locale in the interface and remain ISO dates in JSON and filenames.

The PDF viewer persists page choices in `document_pages`. Scanned pages with very
little dark interior content and no confident OCR text are suggested for exclusion;
printed forms and photos are retained. Empty embedded-text pages also require a
visual blank check. Suggested exclusions force review before automatic filing.
Users can restore any suggested page, compare with the original, and preview the
selected PDF. Approval files that selection. Original scans and OCR blocks retain
all source pages and their original numbering; page cuts do not erase extraction
evidence. At least one page must remain. Reprocessing resets page decisions.
