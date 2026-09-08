#!/bin/sh
set -eu

APP_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
FRONTEND_DIR="$APP_DIR/frontend"
BACKEND_DIR="$APP_DIR/backend"
ENV_FILE="$APP_DIR/.env"
DATABASE_FILE="$BACKEND_DIR/data/account-hub.db"

if [ -f "$ENV_FILE" ]; then
    set -a
    # shellcheck disable=SC1090
    . "$ENV_FILE"
    set +a
fi

if ! command -v go >/dev/null 2>&1; then
    echo "Go is required but was not found in PATH." >&2
    exit 1
fi

if ! command -v pnpm >/dev/null 2>&1; then
    echo "pnpm is required but was not found in PATH." >&2
    exit 1
fi

if ! command -v node >/dev/null 2>&1; then
    echo "Node.js is required but was not found in PATH." >&2
    exit 1
fi

# Keep explicit environment/.env settings and custom go env configurations.
# Go's default comma-separated proxy list does not fall back on timeouts.
if [ -z "${GOPROXY:-}" ]; then
    GOPROXY=$(go env GOPROXY)
    case "$GOPROXY" in
        ""|"https://proxy.golang.org,direct")
            GOPROXY='https://goproxy.cn|https://proxy.golang.org|direct'
            ;;
    esac
fi
export GOPROXY

if [ ! -f "$DATABASE_FILE" ] && [ -z "${ADMIN_PASSWORD:-}" ]; then
    echo "ADMIN_PASSWORD is required when initializing an empty database." >&2
    echo "Export it before running, or copy .env.example to .env and set a strong password." >&2
    exit 1
fi

echo "Downloading backend dependencies..."
if ! (
    cd "$BACKEND_DIR"
    go mod download
); then
    echo "Failed to download Go dependencies. Check the network or set GOPROXY in .env, then run this script again." >&2
    exit 1
fi

BACKEND_BINARY="$BACKEND_DIR/build/account-hub$(go env GOEXE)"
echo "Building Account Hub API..."
mkdir -p "$BACKEND_DIR/build"
if ! (
    cd "$BACKEND_DIR"
    go build -trimpath -o "$BACKEND_BINARY" ./cmd/account-hub
); then
    echo "Backend build failed. Fix the error above, then run this script again." >&2
    exit 1
fi

if [ ! -d "$FRONTEND_DIR/node_modules" ]; then
    echo "Installing frontend dependencies..."
    (
        cd "$FRONTEND_DIR"
        pnpm install --ignore-scripts
    )
fi

FRONTEND_PID=""
BACKEND_PID=""

cleanup() {
    for CHILD_PID in "$FRONTEND_PID" "$BACKEND_PID"; do
        if [ -n "$CHILD_PID" ] && kill -0 "$CHILD_PID" 2>/dev/null; then
            kill "$CHILD_PID" 2>/dev/null || true
            wait "$CHILD_PID" 2>/dev/null || true
        fi
    done
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "Starting Account Hub API at http://0.0.0.0:8500"
(
    cd "$BACKEND_DIR"
    exec "$BACKEND_BINARY"
) &
BACKEND_PID=$!

echo "Waiting for Account Hub API to be ready..."
READY_ATTEMPTS=0
while :; do
    if ! kill -0 "$BACKEND_PID" 2>/dev/null; then
        echo "Backend exited before becoming ready. Check the error above." >&2
        wait "$BACKEND_PID" || exit "$?"
        exit 1
    fi
    if node -e 'fetch("http://127.0.0.1:8500/health/ready", {signal: AbortSignal.timeout(1000)}).then(async response => {const body = await response.json(); process.exit(response.ok && body.success ? 0 : 1)}).catch(() => process.exit(1))'; then
        # Also detect an early exit if another process already occupies 8500.
        if kill -0 "$BACKEND_PID" 2>/dev/null; then
            break
        fi
        continue
    fi
    READY_ATTEMPTS=$((READY_ATTEMPTS + 1))
    if [ "$READY_ATTEMPTS" -ge 30 ]; then
        echo "Backend did not become ready on port 8500. Check its startup logs." >&2
        exit 1
    fi
    sleep 1
done

echo "Starting Account Hub frontend at http://localhost:8501"
(
    cd "$FRONTEND_DIR"
    exec pnpm dev
) &
FRONTEND_PID=$!

while kill -0 "$BACKEND_PID" 2>/dev/null && kill -0 "$FRONTEND_PID" 2>/dev/null; do
    sleep 1
done
if ! kill -0 "$BACKEND_PID" 2>/dev/null; then
    wait "$BACKEND_PID"
else
    wait "$FRONTEND_PID"
fi
