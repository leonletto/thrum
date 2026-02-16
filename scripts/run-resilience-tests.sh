#!/bin/bash
set -euo pipefail
echo "Running resilience tests..."
go test -tags=resilience ./tests/resilience/ -v -timeout 10m -count=1
echo ""
echo "Running benchmarks..."
go test -tags=resilience ./tests/resilience/ -bench=. -benchmem -count=3 -timeout 10m
