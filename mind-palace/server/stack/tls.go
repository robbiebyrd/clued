package stack

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Certificate lifetimes. The CA outlives several leaf renewals; the leaf
// stays under the 825 days Apple's trust store accepts.
const (
	caLifetime   = 10 * 365 * 24 * time.Hour
	leafLifetime = 2 * 365 * 24 * time.Hour
)

// writeCertificates generates a private CA and a server certificate signed
// by it for hosts, and writes tls/ca.pem, tls/ca-key.pem, tls/cert.pem and
// tls/key.pem under dir (the keys with mode 0600). The CA is what clients
// put in serverCaFile; the server (nginx) presents cert.pem with key.pem.
// It returns the paths it wrote.
func writeCertificates(dir string, hosts []string, hostname string, now time.Time) ([]string, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("stack: generate CA key: %w", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "clued server CA (" + hostname + ")", Organization: []string{"clued"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caLifetime),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("stack: create CA certificate: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("stack: generate server key: %w", err)
	}
	names, ips := splitHosts(hosts)
	leafTemplate := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "clued server", Organization: []string{"clued"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
		IPAddresses:  ips,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("stack: create server certificate: %w", err)
	}

	caKeyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		return nil, err
	}
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, err
	}
	files := []struct {
		name string
		mode os.FileMode
		pem  *pem.Block
	}{
		{CAFile, 0o644, &pem.Block{Type: "CERTIFICATE", Bytes: caDER}},
		{CAKeyFile, 0o600, &pem.Block{Type: "EC PRIVATE KEY", Bytes: caKeyDER}},
		{CertFile, 0o644, &pem.Block{Type: "CERTIFICATE", Bytes: leafDER}},
		{KeyFile, 0o600, &pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER}},
	}
	var written []string
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		// Rewrite the file from scratch so a key never keeps a looser mode.
		_ = os.Remove(path)
		if err := os.WriteFile(path, pem.EncodeToMemory(f.pem), f.mode); err != nil {
			return nil, err
		}
		written = append(written, path)
	}
	return written, nil
}

// serial returns a random 128-bit certificate serial number.
func serial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}
