#!/bin/bash
set -euo pipefail
echo "Running resilience tests (with race detector)..."
go test -tags=resilience ./tests/resilience/ -race -v -timeout 10m -count=1
echo ""
echo "Running benchmarks (without race detector for accurate perf)..."
go test -tags=resilience ./tests/resilience/ -bench=. -benchmem -count=3 -timeout 10m
