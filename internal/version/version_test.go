package version

import (
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	v := Version()
	if v == "" {
		t.Error("Version() returned empty string")
	}
	if v != "dev" {
		t.Errorf("expected 'dev', got '%s'", v)
	}
}

func TestCommit(t *testing.T) {
	c := Commit()
	if c == "" {
		t.Error("Commit() returned empty string")
	}
	if c != "none" {
		t.Errorf("expected 'none', got '%s'", c)
	}
}

func TestDate(t *testing.T) {
	d := Date()
	if d == "" {
		t.Error("Date() returned empty string")
	}
	if d != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", d)
	}
}

func TestFull(t *testing.T) {
	f := Full()
	if f == "" {
		t.Error("Full() returned empty string")
	}
	if !strings.Contains(f, "qumo dev") {
		t.Errorf("Full() should contain 'qumo dev', got: %s", f)
	}
	if !strings.Contains(f, "commit: none") {
		t.Errorf("Full() should contain 'commit: none', got: %s", f)
	}
	if !strings.Contains(f, "built:  unknown") {
		t.Errorf("Full() should contain 'built:  unknown', got: %s", f)
	}
}

func TestShort(t *testing.T) {
	s := Short()
	if s == "" {
		t.Error("Short() returned empty string")
	}
	if s != "qumo dev" {
		t.Errorf("expected 'qumo dev', got '%s'", s)
	}
}
