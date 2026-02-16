//go:build resilience

// Package resilience contains integration and stress tests for the Thrum daemon.
// These tests require the "resilience" build tag:
//
//	go test -tags=resilience ./tests/resilience/ -v -timeout 5m
//
// To regenerate the test fixture:
//
//go:generate go run ../../internal/testgen/cmd/ -output testdata/thrum-fixture.tar.gz -seed 42
package resilience
