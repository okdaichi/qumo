package controller

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// CertPaths holds the PEM file paths for a generated self-signed certificate.
type CertPaths struct {
	Cert string
	Key  string
}

const (
	defaultCertFile = "cert.pem"
	defaultKeyFile  = "key.pem"
)

// EnsureCerts generates a self-signed ECDSA certificate in dir if either
// cert.pem or key.pem are missing. Returns the paths to the cert and key files.
func EnsureCerts(dir string) (*CertPaths, error) {
	certPath := filepath.Join(dir, defaultCertFile)
	keyPath := filepath.Join(dir, defaultKeyFile)

	// Check if both files already exist.
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return &CertPaths{Cert: certPath, Key: keyPath}, nil
		}
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir %q: %w", dir, err)
	}

	// Generate ECDSA P-256 key pair.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	// Build a self-signed CA certificate.
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "qumo-benchctl",
		},
		NotBefore: now.Add(-1 * time.Minute),
		NotAfter:  now.Add(7 * 24 * time.Hour),

		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},

		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}

	// Write cert PEM.
	certFile, err := os.Create(certPath)
	if err != nil {
		return nil, fmt.Errorf("create %q: %w", certPath, err)
	}
	defer certFile.Close()
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		return nil, fmt.Errorf("encode cert PEM: %w", err)
	}

	// Write key PEM (PKCS8).
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	keyFile, err := os.Create(keyPath)
	if err != nil {
		return nil, fmt.Errorf("create %q: %w", keyPath, err)
	}
	defer keyFile.Close()
	if err := pem.Encode(keyFile, &pem.Block{Type: "PRIVATE KEY", Bytes: kder}); err != nil {
		return nil, fmt.Errorf("encode key PEM: %w", err)
	}

	return &CertPaths{Cert: certPath, Key: keyPath}, nil
}
