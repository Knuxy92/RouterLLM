package admin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

// maxSerial bounds certificate serial numbers to 128 bits (RFC 5280 cap is 20
// octets; 128 bits keeps them comfortably inside and unguessable).
var maxSerial = new(big.Int).Lsh(big.NewInt(1), 128)

// EnsureCertificate loads the TLS key pair from certPath/keyPath, generating a
// self-signed certificate there when the files don't exist yet. Self-signed is
// the point: a LAN admin console has no CA, and the browser's one-time warning
// is cheaper than running one. Restart keeps the same cert, so the warning is
// a once-per-cert event; delete the files to mint a fresh one.
func EnsureCertificate(certPath, keyPath string) (tls.Certificate, error) {
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			return tls.LoadX509KeyPair(certPath, keyPath)
		}
	}

	cert, key, err := generateSelfSigned()
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certPath, cert, 0o600); err != nil {
		return tls.Certificate{}, fmt.Errorf("cannot write certificate: %w", err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return tls.Certificate{}, fmt.Errorf("cannot write key: %w", err)
	}

	return tls.X509KeyPair(cert, key)
}

func generateSelfSigned() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, maxSerial)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "RouterLLM Admin"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().AddDate(0, 0, 825), // 825 days: the longest span iOS trusts for certs
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		// Self-signed single cert: CA flag lets users import it as a trusted
		// root (browser/curl) instead of clicking through warnings forever.
		IsCA:        true,
		DNSNames:    []string{"localhost"},
		IPAddresses: certSANs(),
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot create certificate: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot marshal key: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, nil
}

// certSANs lists every address the console is plausibly reached on: loopback,
// the machine hostname, and all routable interface addresses — so accepting
// the browser warning is the only manual step, and `curl --cacert` verifies.
func certSANs() []net.IP {
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	seen := map[string]bool{"127.0.0.1": true, "::1": true}
	add := func(ip net.IP) {
		if ip == nil || seen[ip.String()] {
			return
		}
		seen[ip.String()] = true
		ips = append(ips, ip)
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		if resolved, err := net.LookupIP(host); err == nil {
			for _, ip := range resolved {
				add(ip)
			}
		}
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}
			add(ip)
		}
	}

	return ips
}
