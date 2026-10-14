package stack

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Certificate lifetimes. The CA outlives several leaf renewals; the leaf
// stays under the 825 days Apple's trust store accepts.
const (
	caLifetime   = 10 * 365 * 24 * time.Hour
	leafLifetime = 2 * 365 * 24 * time.Hour
	// caRenewBefore is how long before its end a CA is replaced rather
	// than reused.
	caRenewBefore = 30 * 24 * time.Hour
)

// The outcomes for the CA.
const (
	CAGenerated = "generated"
	CAKept      = "kept"
)

// ca is the private CA of a stack.
type ca struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	der  []byte
}

// writeCertificates writes the server certificate (tls/cert.pem and
// tls/key.pem, the key with mode 0600) for hosts, signed by the stack's CA.
// The CA under dir (tls/ca.pem and tls/ca-key.pem) is reused when it still
// covers every host and has more than 30 days left, unless newCA; otherwise
// a new one is generated, so a CA the machine already trusts keeps working
// across renewals. The CA is name-constrained to the hosts it was made for:
// trusted on this machine, it can vouch for this server and nothing else.
// It returns the paths it wrote and whether the CA was generated or kept.
func writeCertificates(dir string, hosts []string, hostname string, now time.Time, newCA bool) ([]string, string, error) {
	var authority *ca
	outcome := CAKept
	if !newCA {
		if existing, err := loadCA(dir); err == nil && covers(existing.cert, hosts) && now.Add(caRenewBefore).Before(existing.cert.NotAfter) {
			authority = existing
		}
	}
	var written []string
	if authority == nil {
		created, err := createCA(hosts, hostname, now)
		if err != nil {
			return nil, "", err
		}
		authority, outcome = created, CAGenerated
		keyDER, err := x509.MarshalECPrivateKey(created.key)
		if err != nil {
			return nil, "", err
		}
		paths, err := writePEMs(dir, []pemFile{
			{CAFile, 0o644, &pem.Block{Type: "CERTIFICATE", Bytes: created.der}},
			{CAKeyFile, 0o600, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}},
		})
		if err != nil {
			return nil, "", err
		}
		written = append(written, paths...)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("stack: generate server key: %w", err)
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
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, authority.cert, &leafKey.PublicKey, authority.key)
	if err != nil {
		return nil, "", fmt.Errorf("stack: create server certificate: %w", err)
	}
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, "", err
	}
	paths, err := writePEMs(dir, []pemFile{
		{CertFile, 0o644, &pem.Block{Type: "CERTIFICATE", Bytes: leafDER}},
		{KeyFile, 0o600, &pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER}},
	})
	if err != nil {
		return nil, "", err
	}
	return append(written, paths...), outcome, nil
}

// createCA makes a CA whose name constraints permit exactly the hosts:
// their DNS names (and names below them) and their addresses.
func createCA(hosts []string, hostname string, now time.Time) (*ca, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("stack: generate CA key: %w", err)
	}
	names, ips := splitHosts(hosts)
	var ranges []*net.IPNet
	for _, ip := range ips {
		bits := 128
		if ip.To4() != nil {
			ip, bits = ip.To4(), 32
		}
		ranges = append(ranges, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	template := &x509.Certificate{
		SerialNumber:                serial(),
		Subject:                     pkix.Name{CommonName: "clued server CA (" + hostname + ")", Organization: []string{"clued"}},
		NotBefore:                   now.Add(-time.Hour),
		NotAfter:                    now.Add(caLifetime),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLenZero:              true,
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         names,
		PermittedIPRanges:           ranges,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("stack: create CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &ca{cert: cert, key: key, der: der}, nil
}

// loadCA reads the stack's CA and its key.
func loadCA(dir string) (*ca, error) {
	cert, err := ReadCA(filepath.Join(dir, CAFile))
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, CAKeyFile))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("stack: CA key is not PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return nil, errors.New("stack: CA key does not match the CA certificate")
	}
	return &ca{cert: cert, key: key, der: cert.Raw}, nil
}

// ReadCA reads a PEM CA certificate.
func ReadCA(path string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("stack: %s holds no PEM certificate", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("stack: %s is not a CA certificate", path)
	}
	return cert, nil
}

// covers reports whether a CA may sign for every host: a CA without name
// constraints (one an older init made) covers anything.
func covers(cert *x509.Certificate, hosts []string) bool {
	if len(cert.PermittedDNSDomains) == 0 && len(cert.PermittedIPRanges) == 0 {
		return true
	}
	names, ips := splitHosts(hosts)
	for _, n := range names {
		if !slices.ContainsFunc(cert.PermittedDNSDomains, func(d string) bool {
			return n == d || strings.HasSuffix(n, "."+d)
		}) {
			return false
		}
	}
	for _, ip := range ips {
		if !slices.ContainsFunc(cert.PermittedIPRanges, func(r *net.IPNet) bool { return r.Contains(ip) }) {
			return false
		}
	}
	return true
}

// Fingerprint is the SHA-256 of a certificate, hex.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// sha1Fingerprint is what macOS's security tool names a certificate by.
func sha1Fingerprint(cert *x509.Certificate) string {
	sum := sha1.Sum(cert.Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

type pemFile struct {
	name string
	mode os.FileMode
	pem  *pem.Block
}

func writePEMs(dir string, files []pemFile) ([]string, error) {
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
