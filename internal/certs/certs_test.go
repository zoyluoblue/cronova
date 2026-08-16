package certs

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
)

func TestCARoundTrip(t *testing.T) {
	dir := t.TempDir()
	ca1, created, err := LoadOrCreateCA(dir)
	if err != nil || !created {
		t.Fatalf("mint: created=%v err=%v", created, err)
	}
	ca2, created, err := LoadOrCreateCA(dir)
	if err != nil || created {
		t.Fatalf("reload: created=%v err=%v", created, err)
	}
	if !ca1.Cert.Equal(ca2.Cert) {
		t.Fatal("reloaded CA differs from minted CA")
	}
}

func TestSignWorkerCSR(t *testing.T) {
	ca, _, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	csrPEM, keyPEM, err := NewCSR()
	if err != nil {
		t.Fatal(err)
	}
	certPEM, err := ca.SignWorkerCSR(csrPEM, "wk_abc123", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "wk_abc123" {
		t.Errorf("CN = %q, want wk_abc123 (CSR subject must be ignored)", cert.Subject.CommonName)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Errorf("issued cert does not verify against CA: %v", err)
	}
	// The worker-side key must pair with the issued cert.
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Errorf("cert/key pair: %v", err)
	}
}

func TestSignRejectsGarbage(t *testing.T) {
	ca, _, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ca.SignWorkerCSR([]byte("not a csr"), "w", time.Hour); err == nil {
		t.Error("garbage CSR accepted")
	}
}

func TestServerCert(t *testing.T) {
	ca, _, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := ca.ServerCert([]string{"scheduler.example.com", "10.0.0.5"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatalf("server pair: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	cert, _ := x509.ParseCertificate(block.Bytes)
	if err := cert.VerifyHostname("scheduler.example.com"); err != nil {
		t.Errorf("hostname: %v", err)
	}
	if err := cert.VerifyHostname("10.0.0.5"); err != nil {
		t.Errorf("ip: %v", err)
	}
}
