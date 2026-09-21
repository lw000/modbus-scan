// Package web embeds the administration user interface.
package web

import "embed"

// Assets contains all static administration files.
//
//go:embed index.html device.html css/*.css js/*.js
var Assets embed.FS
