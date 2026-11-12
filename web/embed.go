package web

import "embed"

// Files contains the app frontend; EchoKit is resolved separately at runtime.
//
//go:embed static
var Files embed.FS
