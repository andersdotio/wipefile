#!/bin/bash

set -e

echo "Building wipefile for common platforms..."

mkdir -p builds

# Linux AMD64
GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -trimpath -o builds/wipefile-linux-amd64 main.go

# Windows AMD64
GOOS=windows GOARCH=amd64 go build -ldflags="-w -s" -trimpath -o builds/wipefile-windows-amd64.exe main.go

# macOS AMD64 (Intel)
GOOS=darwin GOARCH=amd64 go build -ldflags="-w -s" -trimpath -o builds/wipefile-macos-amd64 main.go

# macOS ARM64 (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -ldflags="-w -s" -trimpath -o builds/wipefile-macos-arm64 main.go

echo "Build complete:"
ls -lh builds/