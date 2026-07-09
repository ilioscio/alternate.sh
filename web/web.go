package web

import "embed"

// FS holds the embedded web frontend: the page shell plus the ES modules
// under js/ (codecs, call UI, terminal glue). js/test/ and package.json are
// Node-only test scaffolding and deliberately not embedded.
//
//go:embed index.html js/*.js
var FS embed.FS
