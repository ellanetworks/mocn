package server_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ellanetworks/mocn/internal/config"
	"github.com/ellanetworks/mocn/internal/logging/logtest"
	"github.com/ellanetworks/mocn/internal/server"
	"golang.org/x/crypto/bcrypt"
)

func writeCertificate(t *testing.T, dir string) (string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mocn"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")

	for path, block := range map[string]*pem.Block{
		certPath: {Type: "CERTIFICATE", Bytes: der},
		keyPath:  {Type: "PRIVATE KEY", Bytes: keyDER},
	} {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return certPath, keyPath
}

func TestRunServesHTTPS(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeCertificate(t, dir)

	tlsConfig, err := (&config.TLS{Cert: certPath, Key: keyPath}).Load()
	if err != nil {
		t.Fatal(err)
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	rec := &logtest.Recorder{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() {
		done <- server.Run(ctx, server.Config{
			Listener:   ln,
			TLS:        tlsConfig,
			DBPath:     filepath.Join(dir, "mocn.db"),
			BcryptCost: bcrypt.MinCost,
			Logger:     rec.Logger(),
		})
	}()

	t.Cleanup(func() {
		cancel()

		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})

	pemBytes, err := os.ReadFile(certPath) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pemBytes)

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	base := "https://" + ln.Addr().String()

	var resp *http.Response

	deadline := time.Now().Add(5 * time.Second)

	for {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/api/v1/status", nil)
		if err != nil {
			t.Fatal(err)
		}

		resp, err = client.Do(req)
		if err == nil {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("GET over HTTPS: %v", err)
		}

		time.Sleep(50 * time.Millisecond)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d", resp.StatusCode)
	}

	if hsts := resp.Header.Get("Strict-Transport-Security"); hsts == "" {
		t.Error("Strict-Transport-Security missing over HTTPS")
	}

	plain, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/api/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}

	if resp, err := http.DefaultClient.Do(plain); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Error("plain HTTP answered 200 on the HTTPS listener")
		}
	}
}
