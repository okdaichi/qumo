package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// fakeRelease builds a ghRelease with a tar.gz (or .zip on Windows) archive
// asset containing a fake binary, and a matching checksums.txt.
func fakeRelease(t *testing.T, tag string, prerelease bool) (ghRelease, []byte, []byte) {
	t.Helper()

	binaryContent := []byte("#!/bin/sh\necho hello " + tag)
	archiveName := assetName(tag)

	var archiveData []byte
	if strings.HasSuffix(archiveName, ".zip") {
		archiveData = buildZip(t, binaryContent)
	} else {
		archiveData = buildTarGz(t, binaryContent)
	}

	hash := sha256.Sum256(archiveData)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(hash[:]), archiveName)

	rel := ghRelease{
		TagName:    tag,
		Prerelease: prerelease,
		Assets: []ghAsset{
			{Name: archiveName, BrowserDownloadURL: "ARCHIVE_URL"},
			{Name: "checksums.txt", BrowserDownloadURL: "CHECKSUMS_URL"},
		},
	}
	return rel, archiveData, []byte(checksums)
}

func buildTarGz(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	err := tw.WriteHeader(&tar.Header{
		Name: binaryName,
		Size: int64(len(content)),
		Mode: 0o755,
	})
	require.NoError(t, err)
	_, err = tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

func buildZip(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	name := binaryName
	if runtime.GOOS == "windows" {
		name = binaryName + ".exe"
	}
	w, err := zw.Create(name)
	require.NoError(t, err)
	_, err = w.Write(content)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// serveGitHub starts an httptest server that responds to the GitHub release
// API endpoints, returning the provided release(s) and serving archive/checksum
// downloads.
func serveGitHub(t *testing.T, releases []ghRelease, archiveData, checksumData []byte) *httptest.Server {
	t.Helper()

	// Pre-render JSON.
	latestJSON, err := json.Marshal(releases[0])
	require.NoError(t, err)
	listJSON, err := json.Marshal(releases)
	require.NoError(t, err)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			w.Write(latestJSON)
		case strings.HasSuffix(r.URL.Path, "/releases"):
			w.Header().Set("Content-Type", "application/json")
			w.Write(listJSON)
		case r.URL.Path == "/archive":
			w.Write(archiveData)
		case r.URL.Path == "/checksums":
			w.Write(checksumData)
		default:
			http.NotFound(w, r)
		}
	}))
}

// ---------------------------------------------------------------------------
// semver tests
// ---------------------------------------------------------------------------

func TestParseSemver(t *testing.T) {
	tests := map[string]struct {
		input   string
		want    semver
		wantErr bool
	}{
		"standard semver":              {input: "v1.2.3", want: semver{1, 2, 3, ""}, wantErr: false},
		"SemCalVer release":            {input: "v1.0.260903", want: semver{1, 0, 260903, ""}, wantErr: false},
		"SemCalVer pre-release":        {input: "v1.0.260903-rc.1", want: semver{1, 0, 260903, "rc.1"}, wantErr: false},
		"standard semver pre-release":  {input: "v0.5.0-rc.1", want: semver{0, 5, 0, "rc.1"}, wantErr: false},
		"semver without v prefix":      {input: "0.1.0", want: semver{0, 1, 0, ""}, wantErr: false},
		"invalid format non-numeric":   {input: "bad", want: semver{}, wantErr: true},
		"invalid format missing patch": {input: "v1.2", want: semver{}, wantErr: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseSemver(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSemverGreaterThan(t *testing.T) {
	tests := map[string]struct {
		a, b string
		want bool
	}{
		"minor bump greater":                      {a: "v1.1.0", b: "v1.0.0", want: true},
		"same version equal":                      {a: "v1.0.0", b: "v1.0.0", want: false},
		"lower minor less":                        {a: "v0.9.0", b: "v1.0.0", want: false},
		"release beats pre-release":               {a: "v1.0.0", b: "v1.0.0-rc.1", want: true},
		"pre-release less than release":           {a: "v1.0.0-rc.1", b: "v1.0.0", want: false},
		"higher pre-release beats lower":          {a: "v1.0.0-rc.2", b: "v1.0.0-rc.1", want: true},
		"SemCalVer later date beats earlier":      {a: "v1.0.260903", b: "v1.0.260902", want: true},
		"SemCalVer earlier date less than later":  {a: "v1.0.260902", b: "v1.0.260903", want: false},
		"SemCalVer release beats its pre-release": {a: "v1.0.260903", b: "v1.0.260903-rc.1", want: true},
		"major bump beats minor/patch":            {a: "v2.0.0", b: "v1.99.99", want: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			a, err := parseSemver(tt.a)
			require.NoError(t, err)
			b, err := parseSemver(tt.b)
			require.NoError(t, err)
			assert.Equal(t, tt.want, a.greaterThan(b))
		})
	}
}

// ---------------------------------------------------------------------------
// checksum tests
// ---------------------------------------------------------------------------

func TestVerifyChecksum_OK(t *testing.T) {
	data := []byte("hello")
	hash := sha256.Sum256(data)
	checksums := fmt.Sprintf("%s  myfile.tar.gz\n", hex.EncodeToString(hash[:]))
	err := verifyChecksum(data, []byte(checksums), "myfile.tar.gz")
	assert.NoError(t, err)
}

func TestVerifyChecksum_Mismatch(t *testing.T) {
	data := []byte("hello")
	checksums := "0000000000000000000000000000000000000000000000000000000000000000  myfile.tar.gz\n"
	err := verifyChecksum(data, []byte(checksums), "myfile.tar.gz")
	assert.ErrorContains(t, err, "checksum mismatch")
}

func TestVerifyChecksum_MissingEntry(t *testing.T) {
	checksums := "abcd1234  other.tar.gz\n"
	err := verifyChecksum([]byte("x"), []byte(checksums), "myfile.tar.gz")
	assert.ErrorContains(t, err, "no checksum found")
}

// ---------------------------------------------------------------------------
// asset name
// ---------------------------------------------------------------------------

func TestAssetName(t *testing.T) {
	name := assetName("v1.2.3")
	assert.Contains(t, name, "qumo_1.2.3_")
	assert.Contains(t, name, runtime.GOOS)
	assert.Contains(t, name, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		assert.True(t, strings.HasSuffix(name, ".zip"))
	} else {
		assert.True(t, strings.HasSuffix(name, ".tar.gz"))
	}
}

// ---------------------------------------------------------------------------
// findAssets
// ---------------------------------------------------------------------------

func TestFindAssets_OK(t *testing.T) {
	rel := ghRelease{
		TagName: "v1.0.0",
		Assets: []ghAsset{
			{Name: "qumo_1.0.0_linux_amd64.tar.gz", BrowserDownloadURL: "a"},
			{Name: "checksums.txt", BrowserDownloadURL: "b"},
		},
	}
	a, c, err := findAssets(&rel, "qumo_1.0.0_linux_amd64.tar.gz")
	require.NoError(t, err)
	assert.Equal(t, "a", a.BrowserDownloadURL)
	assert.Equal(t, "b", c.BrowserDownloadURL)
}

func TestFindAssets_MissingArchive(t *testing.T) {
	rel := ghRelease{
		TagName: "v1.0.0",
		Assets: []ghAsset{
			{Name: "checksums.txt"},
		},
	}
	_, _, err := findAssets(&rel, "qumo_1.0.0_linux_amd64.tar.gz")
	assert.ErrorContains(t, err, "no asset")
}

func TestFindAssets_MissingChecksum(t *testing.T) {
	rel := ghRelease{
		TagName: "v1.0.0",
		Assets: []ghAsset{
			{Name: "qumo_1.0.0_linux_amd64.tar.gz"},
		},
	}
	_, _, err := findAssets(&rel, "qumo_1.0.0_linux_amd64.tar.gz")
	assert.ErrorContains(t, err, "no checksums.txt")
}

// ---------------------------------------------------------------------------
// extract binary
// ---------------------------------------------------------------------------

func TestExtractFromTarGz(t *testing.T) {
	want := []byte("the binary")
	archive := buildTarGz(t, want)
	got, err := extractFromTarGz(archive)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestExtractFromZip(t *testing.T) {
	want := []byte("the binary")
	archive := buildZip(t, want)
	got, err := extractFromZip(archive)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// ---------------------------------------------------------------------------
// replaceBinary
// ---------------------------------------------------------------------------

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "qumo")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	require.NoError(t, os.WriteFile(exe, []byte("old"), 0o600))

	newContent := []byte("new binary content")
	require.NoError(t, replaceBinary(exe, newContent))

	got, err := os.ReadFile(filepath.Clean(exe))
	require.NoError(t, err)
	assert.Equal(t, newContent, got)
}

// ---------------------------------------------------------------------------
// detectLatest (integration with httptest)
// ---------------------------------------------------------------------------

func TestDetectLatest_Stable(t *testing.T) {
	rel, _, _ := fakeRelease(t, "v1.0.0", false)
	srv := serveGitHub(t, []ghRelease{rel}, nil, nil)
	defer srv.Close()

	old := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = old }()

	got, err := detectLatest(context.Background(), false)
	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", got.TagName)
}

func TestDetectLatest_Prerelease(t *testing.T) {
	pre := ghRelease{TagName: "v1.1.0-rc.1", Prerelease: true}
	stable := ghRelease{TagName: "v1.0.0", Prerelease: false}
	releases := []ghRelease{pre, stable}

	listJSON, err := json.Marshal(releases)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(listJSON)
	}))
	defer srv.Close()

	old := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = old }()

	got, err := detectLatest(context.Background(), true)
	require.NoError(t, err)
	assert.Equal(t, "v1.1.0-rc.1", got.TagName)
}

// ---------------------------------------------------------------------------
// parseFlags
// ---------------------------------------------------------------------------

func TestParseFlags(t *testing.T) {
	opts, err := parseFlags([]string{"--check"})
	require.NoError(t, err)
	assert.True(t, opts.checkOnly)

	opts, err = parseFlags(nil)
	require.NoError(t, err)
	assert.False(t, opts.checkOnly)
}
