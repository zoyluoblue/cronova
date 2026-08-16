package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoyluo/cronova/internal/certs"
)

// Join performs the one-time bootstrap against a scheduler: generate a local
// keypair, send the CSR with the join token, persist the issued identity into
// stateDir. hubAddrOverride, when non-empty, replaces the server-advertised
// hub address (for NAT/port-forward setups).
func Join(stateDir, serverURL, token, name string, labels map[string]string, hubAddrOverride string) (workerID string, err error) {
	csrPEM, keyPEM, err := certs.NewCSR()
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]any{
		"token":   token,
		"name":    name,
		"labels":  labels,
		"csr_pem": string(csrPEM),
	})
	url := strings.TrimRight(serverURL, "/") + "/api/workers/join"
	cli := &http.Client{Timeout: 30 * time.Second}
	resp, err := cli.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("join %s: %w", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("join refused (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		WorkerID string `json:"worker_id"`
		CertPEM  string `json:"cert_pem"`
		CAPEM    string `json:"ca_pem"`
		HubAddr  string `json:"hub_addr"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("join response: %w", err)
	}
	if out.WorkerID == "" || out.CertPEM == "" || out.CAPEM == "" {
		return "", fmt.Errorf("join response incomplete")
	}
	hubAddr := out.HubAddr
	if hubAddrOverride != "" {
		hubAddr = hubAddrOverride
	}
	if hubAddr == "" {
		return "", fmt.Errorf("server did not advertise a hub address — pass -hub host:port explicitly")
	}

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	files := map[string]struct {
		data []byte
		mode os.FileMode
	}{
		"worker.key": {keyPEM, 0o600},
		"worker.crt": {[]byte(out.CertPEM), 0o644},
		"ca.crt":     {[]byte(out.CAPEM), 0o644},
	}
	for name, f := range files {
		if err := os.WriteFile(filepath.Join(stateDir, name), f.data, f.mode); err != nil {
			return "", err
		}
	}
	id := identity{WorkerID: out.WorkerID, Name: name, Labels: labels, HubAddr: hubAddr}
	b, _ := json.MarshalIndent(id, "", "  ")
	if err := os.WriteFile(filepath.Join(stateDir, "worker.json"), b, 0o600); err != nil {
		return "", err
	}
	return out.WorkerID, nil
}

// Joined reports whether stateDir already holds a worker identity.
func Joined(stateDir string) bool {
	_, err := os.Stat(filepath.Join(stateDir, "worker.json"))
	return err == nil
}
