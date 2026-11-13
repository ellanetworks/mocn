#!/usr/bin/env bash
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
ui="$(cd "$here/.." && pwd)"
build="$here/.build"
port="${ELLA_E2E_PORT:-5030}"

mkdir -p "$build"
rm -f "$build"/mocn.db*

(cd "$ui" && npm run build)
(cd "$ui/.." && go build -o "$build/mocn" ./cmd/mocn)

cat > "$build/mocn.yaml" <<YAML
logging:
  level: info
db:
  path: $build/mocn.db
api:
  address: 127.0.0.1
  port: $port
s1:
  address: 127.0.0.1
  port: ${ELLA_E2E_S1_PORT:-36499}
YAML

exec "$build/mocn" --config "$build/mocn.yaml"
