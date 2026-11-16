#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export GOTOOLCHAIN=local
export CGO_ENABLED=1
printf '%s\n' '=== Crypto Sail AI offline verification ==='
go version
make check
printf '%s\n' '=== Coverage ==='
go test -cover ./...
printf '%s\n' '=== Offline CLI doctor ==='
./bin/sail doctor -db "$(mktemp -d)/verify.db"
printf '%s\n' '=== Verification finished (NO real market, NO orders) ==='
