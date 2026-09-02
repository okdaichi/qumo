package playground

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// assetRefPattern matches the root-relative file references Vite emits into
// dist/index.html (hashed <script src> and <link href> entries). References
// with other schemes (https://, data:) never start with "/" and are skipped.
var assetRefPattern = regexp.MustCompile(`(?:src|href)="(/[^"]+)"`)

// buildAssetsHint explains how to produce the Vite bundles, for users running
// a binary that was built without them.
const buildAssetsHint = "run `mage webbuild` (or: cd playground && deno install && deno task build), then rebuild qumo from source"

// verifyAssets reports whether fsys — the embedded dist tree, sub-rooted at
// its content root — actually contains the files index.html references.
// Binaries built via `go install` embed only the committed placeholder
// index.html, whose hashed /assets/... paths don't exist; serving that
// placeholder yields a white screen with an opaque module MIME-type error in
// the browser (#376). A nil return means the UI is fully bundled.
func verifyAssets(fsys fs.FS) error {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return fmt.Errorf("playground/dist: index.html missing: %w", err)
	}
	var missing []string
	for _, ref := range assetRefPattern.FindAllStringSubmatch(string(index), -1) {
		if _, err := fs.Stat(fsys, strings.TrimPrefix(ref[1], "/")); err != nil {
			missing = append(missing, ref[1])
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("playground/dist: %s referenced by index.html not embedded; %s",
			strings.Join(missing, ", "), buildAssetsHint)
	}
	return nil
}
