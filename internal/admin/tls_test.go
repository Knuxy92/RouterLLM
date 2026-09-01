package admin

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureCertificateGeneratesAndReuses(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "admin-tls.crt")
	keyPath := filepath.Join(dir, "admin-tls.key")

	pair, err := EnsureCertificate(certPath, keyPath)
	if err != nil {
		t.Fatalf("EnsureCertificate() error = %v", err)
	}
	if len(pair.Certificate) == 0 {
		t.Fatal("no certificate returned")
	}

	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if !leaf.IsCA {
		t.Error("self-signed cert should set IsCA so it can be its own issuer")
	}
	if leaf.NotAfter.Sub(leaf.NotBefore) < 800*24*3600*1e9 {
		t.Errorf("validity = %v, want ~825 days", leaf.NotAfter.Sub(leaf.NotBefore))
	}

	wantSANs := map[string]bool{"127.0.0.1": false, "localhost": false}
	for _, ip := range leaf.IPAddresses {
		if _, ok := wantSANs[ip.String()]; ok {
			wantSANs[ip.String()] = true
		}
	}
	for _, dns := range leaf.DNSNames {
		if _, ok := wantSANs[dns]; ok {
			wantSANs[dns] = true
		}
	}
	for san, seen := range wantSANs {
		if !seen {
			t.Errorf("SAN %q missing (got IPs=%v DNS=%v)", san, leaf.IPAddresses, leaf.DNSNames)
		}
	}

	// Files land with restrictive permissions (POSIX only — Windows reports
	// 0666 regardless) and are reused on the next call — a restart must keep
	// serving the same cert, not mint a new one each boot.
	if runtime.GOOS != "windows" {
		for _, path := range []string{certPath, keyPath} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat %s: %v", path, err)
			}
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Errorf("%s mode = %o, want 600", path, perm)
			}
		}
	}

	reloaded, err := EnsureCertificate(certPath, keyPath)
	if err != nil {
		t.Fatalf("second EnsureCertificate() error = %v", err)
	}
	if string(reloaded.Certificate[0]) == "" || &reloaded == &pair {
		t.Fatal("reload should load from disk")
	}
	old, _ := x509.ParseCertificate(pair.Certificate[0])
	fresh, _ := x509.ParseCertificate(reloaded.Certificate[0])
	if !old.Equal(fresh) {
		t.Error("second call regenerated the certificate instead of loading it")
	}

	// A generated pair must form a valid TLS key pair the listener accepts.
	if _, err := tls.X509KeyPair(mustFile(t, certPath), mustFile(t, keyPath)); err != nil {
		t.Fatalf("generated PEMs rejected by tls: %v", err)
	}
}

func mustFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return data
}
