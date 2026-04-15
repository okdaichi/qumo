package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRTMPConfig_GenericKeys(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yaml")

	yaml := `
server:
  serve_address: "0.0.0.0:8443"
  cert_file: "certs/server.crt"
  key_file: "certs/server.key"
ingest:
  ingest_address: ":1935"
`
	if err := os.WriteFile(configFile, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadRTMPConfig(configFile)
	if err != nil {
		t.Fatalf("loadRTMPConfig() error: %v", err)
	}

	if got, want := cfg.IngestAddr, ":1935"; got != want {
		t.Fatalf("IngestAddr = %q, want %q", got, want)
	}
	if got, want := cfg.ServeAddr, "0.0.0.0:8443"; got != want {
		t.Fatalf("ServeAddr = %q, want %q", got, want)
	}
	if got, want := cfg.CertFile, "certs/server.crt"; got != want {
		t.Fatalf("CertFile = %q, want %q", got, want)
	}
	if got, want := cfg.KeyFile, "certs/server.key"; got != want {
		t.Fatalf("KeyFile = %q, want %q", got, want)
	}
}

func TestLoadRTMPConfig_LegacyKeysStillWork(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.yaml")

	yaml := `
server:
  moqt_address: "0.0.0.0:9443"
  cert_file: "certs/server.crt"
  key_file: "certs/server.key"
ingest:
  rtmp_address: ":1936"
`
	if err := os.WriteFile(configFile, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadRTMPConfig(configFile)
	if err != nil {
		t.Fatalf("loadRTMPConfig() error: %v", err)
	}

	if got, want := cfg.IngestAddr, ":1936"; got != want {
		t.Fatalf("IngestAddr = %q, want %q", got, want)
	}
	if got, want := cfg.ServeAddr, "0.0.0.0:9443"; got != want {
		t.Fatalf("ServeAddr = %q, want %q", got, want)
	}
}
