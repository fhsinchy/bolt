package bolt

import "embed"

// FrontendAssets holds the embedded frontend build output.
//
//go:embed all:frontend/dist
var FrontendAssets embed.FS
