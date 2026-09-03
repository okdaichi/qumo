package playground

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// assetRefPattern matches the bundle references Vite emits into
// dist/index.html: the `src` of <script> and `href` of <link> entries pointing
// under /assets/ (hashed filenames). The leading \s keeps it from matching
// attribute-name suffixes (data-src, xlink:href), and the character class
// drops query strings and fragments before the fs.Stat.
var assetRefPattern = regexp.MustCompile(`\s(?:src|href)="(/assets/[^"?#]+)`)

// buildAssetsHint explains how to produce the Vite bundles, for users running
// a binary that was built without them.
const buildAssetsHint = "run `mage webbuild` (or: cd playground && deno install && deno task build), then rebuild qumo from source"

// verifyAssets reports whether fsys — the embedded dist tree, sub-rooted at
// its content root — actually contains the /assets/ bundles index.html
// references. Binaries built from a checkout whose dist was never built embed
// only the placeholder index.html, whose hashed /assets/... paths don't exist;
// serving that placeholder yields a white screen with an opaque module
// MIME-type error in the browser (#376). A nil return means the referenced
// bundles are embedded.
//
// Deliberately narrow: it is a safety net against a bundle-less dist, not a
// full asset inventory. It only sees what index.html references directly, so
// an indirect asset missing from a partial dist (a CSS url() target, a lazy
// chunk) still slips through — CI's "Web UI dist freshness" job is the real
// guarantee that the committed dist is complete.
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
