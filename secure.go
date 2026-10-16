package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Both sides use self-signed certificates and pin each other's fingerprint on
// first contact (trust on first use). Keystrokes cross the network, so an
// unpinned peer is never accepted after pairing.

func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "desksynk")
	return dir, os.MkdirAll(dir, 0o700)
}

func loadOrCreateCert(dir, name string) (tls.Certificate, error) {
	certPath := filepath.Join(dir, name+".crt")
	keyPath := filepath.Join(dir, name+".key")
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "desksynk " + name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(certPath, keyPath)
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// pinVerifier accepts the peer whose fingerprint is stored at pinPath. With no
// pin stored it accepts and stores the first peer it sees.
func pinVerifier(pinPath, peer string) func([][]byte, [][]*x509.Certificate) error {
	return func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) == 0 {
			return errors.New("peer sent no certificate")
		}
		got := fingerprint(raw[0])
		want, err := os.ReadFile(pinPath)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(pinPath, []byte(got+"\n"), 0o600); err != nil {
				return err
			}
			logf("paired with %s %s (pin saved to %s)", peer, short(got), pinPath)
			return nil
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(want)) != got {
			return fmt.Errorf("%s fingerprint %s does not match pinned %s; if you reinstalled it, delete %s to re-pair",
				peer, short(got), short(strings.TrimSpace(string(want))), pinPath)
		}
		return nil
	}
}

func short(fp string) string {
	if len(fp) > 16 {
		return fp[:16]
	}
	return fp
}

func tlsConfig(role string) (*tls.Config, string, error) {
	dir, err := configDir()
	if err != nil {
		return nil, "", err
	}
	cert, err := loadOrCreateCert(dir, role)
	if err != nil {
		return nil, "", err
	}
	peer := map[string]string{"server": "client", "client": "server"}[role]
	cfg := &tls.Config{
		Certificates:          []tls.Certificate{cert},
		MinVersion:            tls.VersionTLS13,
		InsecureSkipVerify:    true, // self-signed; VerifyPeerCertificate pins instead
		ClientAuth:            tls.RequireAnyClientCert,
		VerifyPeerCertificate: pinVerifier(filepath.Join(dir, peer+".pin"), peer),
	}
	return cfg, fingerprint(cert.Certificate[0]), nil
}
