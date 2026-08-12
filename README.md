# Zotero AI Tagger

A high-performance, modular Go CLI application that automatically fetches academic papers from a Zotero library, extracts metadata and PDF full text, optimizes text to minimize LLM token usage (70–80% reduction), extracts taxonomy and controlled microbiological tags via SiliconFlow (OpenAI-compatible API), and updates Zotero items.

---

## Features

- **Zotero REST API Integration**: Direct client with automatic pagination, rate-limit retry (`Retry-After`), and version optimistic locking.
- **PDF Extraction**: Uses `pdftotext` (poppler-utils) with fallback to item abstract note if no PDF attachment exists.
- **70–80% Token Optimization**:
  - Parenthetical and bracketed citation stripping.
  - Bibliography/References section truncation.
  - IMRAD section priority filtering (Abstract > Conclusion > Introduction).
  - Whitespace normalization and configurable token budget truncation.
- **Zero-Token Local Species Pre-Extraction**:
  - Regex pre-extraction for Latin binomials (`Escherichia coli`), genus mentions (`Streptococcus sp.`), and taxonomic suffixes (`-aceae`, `-cocci`, etc.).
  - Deduplicated candidate species passed as context hints to the LLM.
- **Strict Taxonomy Namespacing**:
  - `org:` — binomial species format (e.g., `org:escherichia-coli`).
  - `group:` — broader taxonomic clades or traits (e.g., `group:enterobacteriaceae`).
  - `topic:` — enforced strictly from a configurable `CONTROLLED_TOPICS` list.
- **Idempotency & Safety**:
  - Sentinel tag (`_ai-tagged`) prevents duplicate processing.
  - `--dry-run` flag to preview tags in beautiful terminal tables without modifying Zotero.
  - Configurable RPM, TPM, RPD rate limiting.
- **Container Ready**: Alpine multi-stage Docker build producing a static binary container (~25MB).

---

## Installation & Setup

### Prerequisites
- Go 1.23+
- `poppler-utils` (for local PDF text extraction via `pdftotext`)

### Build Locally

```bash
git clone https://github.com/exterex/zotero-tagger.git
cd zotero-tagger
go build -o zotero-tagger ./cmd/zotero-tagger
```

### Configuration

1. Copy `.env.example` to `.env` and fill in credentials:
```env
ZOTERO_USER_ID=your_user_id
ZOTERO_API_KEY=your_api_key
LLM_API_KEY=your_siliconflow_api_key
```

2. Edit `config/config.toml` to customize allowed item types, token limits, rate limits, and controlled topics:
```toml
[llm]
model_name = "Qwen/Qwen3-8B"
base_url = "https://api.siliconflow.cn/v1"
temperature = 0.0
max_input_tokens = 10000

[tagging.controlled_topics]
topics = [
    "oral microbiology",
    "biofilm",
    "quorum sensing",
    "PCR"
]
```

---

## Usage

### Run Tagging Pipeline

```bash
# Preview tagging for 5 items without modifying Zotero
./zotero-tagger tag --dry-run --limit 5

# Process a specific item by key
./zotero-tagger tag --item ITEM_KEY --dry-run

# Run against a specific collection with 3 concurrent workers
./zotero-tagger tag --collection COLLECTION_KEY --concurrent 3

# Force reprocessing of already tagged items
./zotero-tagger tag --reprocess
```

---

## Running with Docker

```bash
# Build and run containerized tagger
docker compose -f docker/docker-compose.yml up --build
```
