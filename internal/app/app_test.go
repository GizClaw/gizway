package app_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idy/gizway/internal/api"
	"github.com/idy/gizway/internal/app"
	"github.com/idy/gizway/internal/testdb"
)

func TestRunRejectsConflictingStoryFlags(t *testing.T) {
	err := app.Run(context.Background(), app.Config{StoryTestMode: true, Initialize: true})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("Run error = %v", err)
	}
}

func TestRunRejectsMutuallyExclusiveStoryModesAndUnknownSurface(t *testing.T) {
	if err := app.Run(context.Background(), app.Config{StoryTestMode: true, StoryResumeMode: true}); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("mutually exclusive story modes error=%v", err)
	}
	if err := app.Run(context.Background(), app.Config{Surface: api.Surface(99), StoryTestMode: true, PostgreSQLDSN: testdb.NewSchema(t)}); err == nil || !strings.Contains(err.Error(), "surface is required") {
		t.Fatalf("unknown surface error=%v", err)
	}
}

func TestRunRequiresPostgreSQL(t *testing.T) {
	if err := app.Run(context.Background(), app.Config{StoryTestMode: true}); err == nil || !strings.Contains(err.Error(), "PostgreSQL DSN is required") {
		t.Fatalf("missing PostgreSQL error=%v", err)
	}
}

func TestRunStoryServerLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	dsn := testdb.NewSchema(t)
	go func() {
		done <- app.Run(ctx, app.Config{
			Address: "127.0.0.1:0", PostgreSQLDSN: dsn,
			StoryTestMode: true,
		})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}

func TestRunStopsWorkersWhenHTTPListenFails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	dsn := testdb.NewSchema(t)

	done := make(chan error, 1)
	go func() {
		done <- app.Run(context.Background(), app.Config{
			Address: listener.Addr().String(), PostgreSQLDSN: dsn,
			StoryTestMode: true,
		})
	}()
	select {
	case runErr := <-done:
		if runErr == nil || !strings.Contains(runErr.Error(), "serve HTTP") {
			t.Fatalf("Run error = %v, want listener failure", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop workers after listener failure")
	}
}

func TestRunReturnsStorageError(t *testing.T) {
	err := app.Run(context.Background(), app.Config{PostgreSQLDSN: "host=127.0.0.1 port=1 user=gizway dbname=gizway sslmode=disable connect_timeout=1", Initialize: true})
	if err == nil {
		t.Fatal("Run with invalid database path succeeded")
	}
}

func TestRunRequiresValidProductionSecretKey(t *testing.T) {
	if err := app.Run(context.Background(), app.Config{PostgreSQLDSN: testdb.NewSchema(t), Initialize: true}); err == nil || !strings.Contains(err.Error(), "SECRET_ENCRYPTION_KEY is required") {
		t.Fatalf("missing secret key error = %v", err)
	}
	if err := app.Run(context.Background(), app.Config{PostgreSQLDSN: testdb.NewSchema(t), Initialize: true, SecretEncryptionKey: "not-base64"}); err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("invalid secret key error = %v", err)
	}
}

func TestRunRejectsIncompleteProductionIntegrationConfiguration(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	base := func(name string) app.Config {
		return app.Config{
			PostgreSQLDSN:       testdb.NewSchema(t),
			Initialize:          true,
			SecretEncryptionKey: secret,
			CheckoutBaseURL:     "https://pay.example.test",
		}
	}
	tests := []struct {
		name      string
		configure func(*app.Config)
		contains  string
	}{
		{
			name: "AI provider credential",
			configure: func(config *app.Config) {
				config.Surface = api.SurfaceGizWay
				config.AIProviderBaseURL = "https://ai.example.test"
			},
			contains: "AI provider credential",
		},
		{
			name: "payment provider secrets",
			configure: func(config *app.Config) {
				config.PaymentProviderBaseURL = "https://payments.example.test"
			},
			contains: "payment provider credential and callback secret",
		},
		{
			name: "missing checkout URL",
			configure: func(config *app.Config) {
				config.CheckoutBaseURL = ""
			},
			contains: "public checkout base URL is required",
		},
		{
			name: "insecure checkout URL",
			configure: func(config *app.Config) {
				config.CheckoutBaseURL = "http://pay.example.test"
			},
			contains: "absolute HTTPS URL",
		},
		{
			name: "relative checkout URL",
			configure: func(config *app.Config) {
				config.CheckoutBaseURL = "/checkout"
			},
			contains: "absolute HTTPS URL",
		},
		{
			name: "risk provider credential",
			configure: func(config *app.Config) {
				config.RiskProviderBaseURL = "https://risk.example.test"
			},
			contains: "risk provider credential",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base(t.Name())
			test.configure(&config)
			err := app.Run(context.Background(), config)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Run error=%v, want substring %q", err, test.contains)
			}
		})
	}
}

func TestProductionCompositionRootsHaveIndependentLifecycles(t *testing.T) {
	pki := newApplicationTestPKI(t)
	secret := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	tests := []struct {
		name   string
		config app.Config
	}{
		{
			name: "GizPay has no Gateway service",
			config: app.Config{
				Surface: api.SurfaceGizPay, Address: "127.0.0.1:0", PostgreSQLDSN: testdb.NewSchema(t), Initialize: true,
				SecretEncryptionKey: secret, CheckoutBaseURL: "https://checkout.gizway.test",
				TLSCertificateFile: pki.serverCertificate, TLSPrivateKeyFile: pki.serverPrivateKey, GatewayClientCAFile: pki.caCertificate,
				PowerSyncURL: "https://sync.gizway.test", PowerSyncAudience: "gizpay", PowerSyncKeyID: "key-1", PowerSyncSigningKey: secret,
			},
		},
		{
			name: "GizWay has local Gateway service",
			config: app.Config{
				Surface: api.SurfaceGizWay, Address: "127.0.0.1:0", PostgreSQLDSN: testdb.NewSchema(t), Initialize: true,
				SecretEncryptionKey: secret, NodeID: "gw-cn-test", Region: "cn", GizPayInternalBaseURL: "https://127.0.0.1:1",
				GizPayMTLSCertificateFile: pki.clientCertificate, GizPayMTLSPrivateKeyFile: pki.clientPrivateKey, GizPayMTLSServerCAFile: pki.caCertificate,
			},
		},
		{
			name: "GizPay composes configured payment and risk adapters",
			config: app.Config{
				Surface: api.SurfaceGizPay, Address: "127.0.0.1:0", PostgreSQLDSN: testdb.NewSchema(t), Initialize: true,
				SecretEncryptionKey: secret, CheckoutBaseURL: "https://checkout.gizway.test",
				PaymentProviderBaseURL: "https://payments.gizway.test", PaymentProviderCredential: "payment-secret", PaymentCallbackSecret: "callback-secret",
				RiskProviderBaseURL: "https://risk.gizway.test", RiskProviderCredential: "risk-secret",
				TLSCertificateFile: pki.serverCertificate, TLSPrivateKeyFile: pki.serverPrivateKey, GatewayClientCAFile: pki.caCertificate,
			},
		},
		{
			name: "GizWay composes configured process-default AI adapter",
			config: app.Config{
				Surface: api.SurfaceGizWay, Address: "127.0.0.1:0", PostgreSQLDSN: testdb.NewSchema(t), Initialize: true,
				SecretEncryptionKey: secret, AIProviderBaseURL: "https://ai.gizway.test", AIProviderCredential: "ai-secret",
				NodeID: "gw-cn-test", Region: "cn", GizPayInternalBaseURL: "https://127.0.0.1:1",
				GizPayMTLSCertificateFile: pki.clientCertificate, GizPayMTLSPrivateKeyFile: pki.clientPrivateKey, GizPayMTLSServerCAFile: pki.caCertificate,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- app.Run(ctx, test.config) }()
			time.Sleep(50 * time.Millisecond)
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Run: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("production composition did not stop after cancellation")
			}
		})
	}
}

func TestRunValidatesPowerSyncAsOneSecurityConfiguration(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	for _, test := range []struct {
		name      string
		configure func(*app.Config)
		contains  string
	}{
		{name: "partial", configure: func(config *app.Config) { config.PowerSyncURL = "https://sync.gizway.test" }, contains: "configured together"},
		{name: "insecure URL", configure: func(config *app.Config) {
			config.PowerSyncURL, config.PowerSyncAudience, config.PowerSyncKeyID, config.PowerSyncSigningKey = "http://sync.gizway.test", "gizpay", "key-1", secret
		}, contains: "absolute HTTPS URL"},
		{name: "short signing key", configure: func(config *app.Config) {
			config.PowerSyncURL, config.PowerSyncAudience, config.PowerSyncKeyID, config.PowerSyncSigningKey = "https://sync.gizway.test", "gizpay", "key-1", base64.StdEncoding.EncodeToString([]byte("short"))
		}, contains: "at least 32 bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := app.Config{
				Surface: api.SurfaceGizPay, PostgreSQLDSN: testdb.NewSchema(t), Initialize: true,
				SecretEncryptionKey: secret, CheckoutBaseURL: "https://checkout.gizway.test",
			}
			test.configure(&config)
			if err := app.Run(t.Context(), config); err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Run error=%v, want %q", err, test.contains)
			}
		})
	}
}

func TestRunValidatesRegionalIdentityAndTLSFiles(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	regionalBase := func() app.Config {
		return app.Config{
			Surface: api.SurfaceGizWay, PostgreSQLDSN: testdb.NewSchema(t), Initialize: true,
			SecretEncryptionKey: secret,
		}
	}
	for _, test := range []struct {
		name      string
		configure func(*app.Config)
		contains  string
	}{
		{name: "missing node identity", configure: func(*app.Config) {}, contains: "node ID"},
		{name: "invalid region", configure: func(config *app.Config) { config.NodeID, config.Region = "node", "eu" }, contains: "cn/global"},
		{name: "missing mTLS files", configure: func(config *app.Config) { config.NodeID, config.Region = "node", "cn" }, contains: "node certificate"},
		{name: "unreadable mTLS files", configure: func(config *app.Config) {
			config.NodeID, config.Region = "node", "cn"
			config.GizPayInternalBaseURL = "https://gizpay.invalid"
			config.GizPayMTLSCertificateFile, config.GizPayMTLSPrivateKeyFile, config.GizPayMTLSServerCAFile = "/missing/cert", "/missing/key", "/missing/ca"
		}, contains: "load GizPay client certificate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := regionalBase()
			test.configure(&config)
			if err := app.Run(t.Context(), config); err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Run error=%v, want %q", err, test.contains)
			}
		})
	}

	pki := newApplicationTestPKI(t)
	for _, test := range []struct {
		name      string
		configure func(*app.Config)
		contains  string
	}{
		{name: "incomplete TLS", configure: func(config *app.Config) { config.TLSCertificateFile = pki.serverCertificate }, contains: "certificate, private key"},
		{name: "missing client CA", configure: func(config *app.Config) {
			config.TLSCertificateFile, config.TLSPrivateKeyFile, config.GatewayClientCAFile = pki.serverCertificate, pki.serverPrivateKey, "/missing/ca"
		}, contains: "read Gateway client CA"},
		{name: "invalid client CA", configure: func(config *app.Config) {
			invalid := filepath.Join(t.TempDir(), "invalid-ca")
			if err := os.WriteFile(invalid, []byte("not a certificate"), 0o600); err != nil {
				t.Fatal(err)
			}
			config.TLSCertificateFile, config.TLSPrivateKeyFile, config.GatewayClientCAFile = pki.serverCertificate, pki.serverPrivateKey, invalid
		}, contains: "contains no certificates"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := app.Config{
				Surface: api.SurfaceGizPay, PostgreSQLDSN: testdb.NewSchema(t), Initialize: true,
				SecretEncryptionKey: secret, CheckoutBaseURL: "https://checkout.gizway.test",
			}
			test.configure(&config)
			if err := app.Run(t.Context(), config); err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Run error=%v, want %q", err, test.contains)
			}
		})
	}
}

type applicationTestPKI struct {
	caCertificate, serverCertificate, serverPrivateKey, clientCertificate, clientPrivateKey string
}

func newApplicationTestPKI(t *testing.T) applicationTestPKI {
	t.Helper()
	directory := t.TempDir()
	notBefore := time.Now().Add(-time.Hour)
	notAfter := time.Now().Add(time.Hour)
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Gizway test CA"},
		NotBefore: notBefore, NotAfter: notAfter, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(directory, "ca.crt")
	writePEMFile(t, caPath, "CERTIFICATE", caDER)

	issue := func(name string, serial int64, usages []x509.ExtKeyUsage, configure func(*x509.Certificate)) (string, string) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		certificate := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, NotBefore: notBefore, NotAfter: notAfter,
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usages,
		}
		configure(certificate)
		der, err := x509.CreateCertificate(rand.Reader, certificate, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		certificatePath := filepath.Join(directory, name+".crt")
		privateKeyPath := filepath.Join(directory, name+".key")
		writePEMFile(t, certificatePath, "CERTIFICATE", der)
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		writePEMFile(t, privateKeyPath, "EC PRIVATE KEY", keyDER)
		return certificatePath, privateKeyPath
	}
	serverCertificate, serverPrivateKey := issue("server", 2, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, func(certificate *x509.Certificate) {
		certificate.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	})
	clientCertificate, clientPrivateKey := issue("client", 3, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, func(certificate *x509.Certificate) {
		identity, err := url.Parse("spiffe://gizway/gateway/cn/gw-cn-test")
		if err != nil {
			t.Fatal(err)
		}
		certificate.URIs = []*url.URL{identity}
	})
	return applicationTestPKI{
		caCertificate: caPath, serverCertificate: serverCertificate, serverPrivateKey: serverPrivateKey,
		clientCertificate: clientCertificate, clientPrivateKey: clientPrivateKey,
	}
}

func writePEMFile(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}
