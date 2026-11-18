#!/usr/bin/env bash
set -euo pipefail

DEPS_ONLY=false
if [ "${1:-}" = "--deps-only" ]; then
    DEPS_ONLY=true
fi

echo "==> Downloading Go module dependencies..."
go mod download

# .golangci.yml uses version: "2", so ensure golangci-lint v2 is available.
if ! command -v golangci-lint >/dev/null 2>&1 || ! golangci-lint --version 2>/dev/null | grep -Eq '(^|[[:space:]])v?2\.'; then
    echo "==> Installing golangci-lint v2..."
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
fi

if [ "$DEPS_ONLY" = true ]; then
    echo "==> Dependency setup complete."
    exit 0
fi

echo "==> Configuring repository git pre-commit hook..."
if [ -d .git ] && [ -f .githooks/pre-commit ]; then
    chmod +x .githooks/pre-commit
    git config core.hooksPath .githooks
fi

if [ ! -f .env ] && { [ -z "${ZOTERO_USER_ID:-}" ] || [ -z "${ZOTERO_API_KEY:-}" ] || [ -z "${GEMINI_API_KEY:-}" ]; }; then
    echo ""
    echo "NOTE: Configure your API credentials before running zotero-tagger:"
    echo "  - Option A (Codespaces): Set ZOTERO_USER_ID, ZOTERO_API_KEY, and GEMINI_API_KEY in GitHub Codespaces secrets."
    echo "  - Option B (Local .env): Run 'cp .env.example .env' and fill in your credentials."
fi

echo "==> Codespace setup complete! Run 'make all' to lint, test, and build."
