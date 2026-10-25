
#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${ORIGINAL_TEST_DATABASE_URL:-}" ]]; then
    echo "ERROR: ORIGINAL_TEST_DATABASE_URL is not set." >&2
    echo "Set it to your trailnest_test connection URL." >&2
    exit 1
fi

if [[ -z "${EXPIRY_DATABASE_URL:-}" ]]; then
    echo "ERROR: EXPIRY_DATABASE_URL is not set." >&2
    echo "Set it to your trailnest_expiry_test connection URL." >&2
    exit 1
fi

echo "==> Running booking tests..."
TEST_DATABASE_URL="$ORIGINAL_TEST_DATABASE_URL" \
    go test ./internal/booking -count=1

echo "==> Running payment tests..."
TEST_DATABASE_URL="$ORIGINAL_TEST_DATABASE_URL" \
    go test ./internal/payment -count=1

echo "==> Running reservation expiry tests..."
TEST_DATABASE_URL="$EXPIRY_DATABASE_URL" \
    go test ./internal/reservation -count=1

echo "==> Running remaining packages..."
TEST_DATABASE_URL="" go test ./cmd/... ./internal/activity ./internal/auth \
    ./internal/config ./internal/database ./internal/http/... \
    ./internal/user ./internal/wishlist

echo "All TrailNest tests passed!"
