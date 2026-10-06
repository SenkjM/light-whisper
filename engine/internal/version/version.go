// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package version holds build and API version information.
package version

// Version is the engine build version; override with
// -ldflags "-X github.com/SenkjM/light-whisper/engine/internal/version.Version=..."
var Version = "0.0.0-dev"

// APIVersion is the HTTP/WS contract major version checked by the shell
// (GET /health). Bump only on breaking changes.
const APIVersion = 1
