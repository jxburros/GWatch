// Package web holds the one part of the web interface that a second binary
// needs: the fonts and the agent's mark, which gwatch-agent's control panel
// serves so that it looks like the rest of GWatch without a second copy of
// them in the repository. The interface itself is embedded by the main package.
package web

import "embed"

// Assets is the fonts directory and the agent's logo.
//
//go:embed fonts agent-logo.png
var Assets embed.FS
