// Package assets embeds the binaries runner machines download from the
// control plane: the cache proxy, built for Linux on both architectures by
// `pnpm build` and the Dockerfile. A development checkout without them
// still serves everything else; cache-enabled runners then log that the
// proxy is unavailable and run their jobs without it.
package assets

import (
	"embed"
	"io/fs"
)

//go:embed all:bin
var bin embed.FS

// CacheProxy returns the cache proxy binary for an OS and architecture.
func CacheProxy(os, arch string) ([]byte, bool) {
	data, err := fs.ReadFile(bin, "bin/cacheproxy-"+os+"-"+arch)
	if err != nil {
		return nil, false
	}

	return data, true
}
