#!/usr/bin/env bash
# Run with: ./scripts/test.sh
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

for command in docker go python3; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "ERROR: Required command not found: $command" >&2
        exit 1
    fi
done

if [[ ! -f .env.docker ]]; then
    echo "ERROR: .env.docker was not found in $ROOT_DIR" >&2
    exit 1
fi

echo "==> Starting Docker PostgreSQL..."
docker compose up -d db

echo "==> Waiting for PostgreSQL to become ready..."
ready=0

for attempt in $(seq 1 30); do
    if docker compose exec -T db \
        sh -lc 'pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"' \
        >/dev/null 2>&1; then
        ready=1
        break
    fi

    sleep 2
done

if [[ "$ready" -ne 1 ]]; then
    echo "ERROR: Docker PostgreSQL did not become ready." >&2
    docker compose logs --tail=50 db >&2
    exit 1
fi

echo "==> Configuring Docker test database URLs..."

database_urls="$(
    python3 - <<'PY'
from pathlib import Path
from urllib.parse import urlsplit, urlunsplit

values = {}

for line in Path(".env.docker").read_text().splitlines():
    line = line.strip()

    if not line or line.startswith("#") or "=" not in line:
        continue

    key, value = line.split("=", 1)

    if key in {"TEST_DATABASE_URL"}:
        value = value.strip()

        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]

        values[key] = value

raw_url = values.get("TEST_DATABASE_URL", "")
if not raw_url:
    raise SystemExit("ERROR: TEST_DATABASE_URL is missing from .env.docker")

parsed = urlsplit(raw_url)

if parsed.scheme not in {"postgres", "postgresql"}:
    raise SystemExit("ERROR: TEST_DATABASE_URL must be a PostgreSQL URL")

if not parsed.hostname or parsed.path.rstrip("/") != "/trailnest_test":
    raise SystemExit(
        "ERROR: Expected TEST_DATABASE_URL to target trailnest_test"
    )

# Preserve URL-encoded credentials and query parameters, but connect
# from WSL through Docker's published host port.
auth = parsed.netloc.rsplit("@", 1)[0] + "@" if "@" in parsed.netloc else ""
netloc = auth + "127.0.0.1:5436"

main_url = urlunsplit(
    (parsed.scheme, netloc, "/trailnest_test", parsed.query, parsed.fragment)
)
expiry_url = urlunsplit(
    (
        parsed.scheme,
        netloc,
        "/trailnest_expiry_test",
        parsed.query,
        parsed.fragment,
    )
)

print(main_url)
print(expiry_url)
PY
)"

mapfile -t database_url_array <<< "$database_urls"

if [[ "${#database_url_array[@]}" -ne 2 ]] ||
   [[ -z "${database_url_array[0]}" ]] ||
   [[ -z "${database_url_array[1]}" ]]; then
    echo "ERROR: Could not configure the Docker test database URLs." >&2
    exit 1
fi

export TEST_DATABASE_URL="${database_url_array[0]}"
export RESERVATION_TEST_DATABASE_URL="${database_url_array[1]}"

echo "==> Verifying required test databases..."

databases="$(
    docker compose exec -T db \
        sh -lc 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At' <<'SQL'
SELECT datname
FROM pg_database
WHERE datname IN ('trailnest_test', 'trailnest_expiry_test')
ORDER BY datname;
SQL
)"

for database in trailnest_test trailnest_expiry_test; do
    if ! grep -Fxq "$database" <<< "$databases"; then
        echo "ERROR: Required database '$database' does not exist in Docker." >&2
        echo "No databases have been created or modified by this script." >&2
        exit 1
    fi
done

echo "    trailnest_test: Docker PostgreSQL"
echo "    trailnest_expiry_test: Docker PostgreSQL"

echo "==> Running complete Go test suite..."
go test ./... -count=1

echo "All TrailNest tests passed!"
