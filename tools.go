//go:build tools

package main

// Pinned CLI tools (kept across `go mod tidy`).
import (
	_ "github.com/swaggo/swag/cmd/swag"
)
