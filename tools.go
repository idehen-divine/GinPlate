//go:build tools

package main

// Pinned CLI tools (kept across `go mod tidy`, invoked via `go run`).
import (
	_ "github.com/swaggo/swag/cmd/swag"
)
