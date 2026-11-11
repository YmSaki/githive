// Package schemas embeds the authoritative JSON schemas used by spec/validate.py.
package schemas

import "embed"

// Files contains the exact schema sources shipped with this module.
//
//go:embed *.json
var Files embed.FS
