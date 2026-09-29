#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

echo "==> Running Failsafe-Go Interactive Demo..."
go run ./cmd/demo/main.go

echo "==> Running Complete Parameterized Test Suite with Race Detector..."
go test -v -race ./...
