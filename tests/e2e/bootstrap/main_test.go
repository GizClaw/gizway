package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

func TestBootstrapBuildsThreeIndependentServiceDatabases(t *testing.T) {
	centralDSN := initializedServiceDatabase(t, storage.OpenGizPayPostgreSQL)
	pki := t.TempDir()
	writeGatewayCertificate(t, pki, "cn", 1)
	writeGatewayCertificate(t, pki, "global", 2)
	if err := run("central", centralDSN, pki, ""); err != nil {
		t.Fatalf("bootstrap central: %v", err)
	}

	central, err := storage.OpenGizPayPostgreSQL(centralDSN, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = central.Close() })
	var nodes, keys int
	if err := central.SQL.Get(&nodes, `SELECT COUNT(*) FROM gateway_nodes`); err != nil {
		t.Fatal(err)
	}
	if err := central.SQL.Get(&keys, `SELECT COUNT(*) FROM api_keys WHERE id IN ('e2e-key','e2e-key-two')`); err != nil {
		t.Fatal(err)
	}
	var sessions, merchantServices, riskDecisions int
	if err := central.SQL.Get(&sessions, `SELECT COUNT(*) FROM user_sessions WHERE id IN ('e2e-session','e2e-session-two')`); err != nil {
		t.Fatal(err)
	}
	if err := central.SQL.Get(&merchantServices, `SELECT COUNT(*) FROM merchant_services`); err != nil {
		t.Fatal(err)
	}
	if err := central.SQL.Get(&riskDecisions, `SELECT COUNT(*) FROM risk_decisions`); err != nil {
		t.Fatal(err)
	}
	if nodes != 2 || keys != 2 || sessions != 2 || merchantServices != 0 || riskDecisions != 0 {
		t.Fatalf("central fixtures: nodes=%d keys=%d sessions=%d merchant_services=%d risk_decisions=%d", nodes, keys, sessions, merchantServices, riskDecisions)
	}

	// Run both regions through the same production bootstrap function but give
	// each one a distinct schema. This is the regression proof that Catalog and
	// local administrator ownership do not leak between CN and Global.
	for _, region := range []string{"cn", "global"} {
		t.Run(region, func(t *testing.T) {
			dsn := initializedServiceDatabase(t, storage.OpenGizWayPostgreSQL)
			if err := run(region, dsn, "", "https://"+region+".provider.invalid"); err != nil {
				t.Fatalf("bootstrap %s: %v", region, err)
			}
			database, err := storage.OpenGizWayPostgreSQL(dsn, false)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			var administrators, models int
			if err := database.SQL.Get(&administrators, `SELECT COUNT(*) FROM administrators WHERE email=$1`, region+"-admin@e2e.invalid"); err != nil {
				t.Fatal(err)
			}
			if err := database.SQL.Get(&models, `SELECT COUNT(*) FROM models WHERE slug=$1`, region+"-model"); err != nil {
				t.Fatal(err)
			}
			if administrators != 1 || models != 1 {
				t.Fatalf("%s fixtures: administrators=%d models=%d", region, administrators, models)
			}
		})
	}
}

// The Compose PKI must be valid for the fixed fixture clock regardless of the
// wall clock on the developer machine or CI runner. This test executes the
// same shell generator mounted by pki-init, then covers the complete central
// registry path: parse, cryptographically verify, register, activate, and
// authenticate the generated Gateway leaf.
func TestGeneratedPKIRegistersActivatesAndAuthenticatesAtFixtureTime(t *testing.T) {
	pki := t.TempDir()
	generator := filepath.Join("..", "pki", "generate.sh")
	command := exec.Command("sh", generator, pki)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate deterministic E2E PKI: %v\n%s", err, output)
	}

	readCertificate := func(name string) *x509.Certificate {
		t.Helper()
		encoded, err := os.ReadFile(filepath.Join(pki, name))
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(encoded)
		if block == nil || block.Type != "CERTIFICATE" {
			t.Fatalf("%s does not contain a certificate", name)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		return certificate
	}

	fixtureTime, err := time.Parse(time.RFC3339Nano, fixtureAt)
	if err != nil {
		t.Fatal(err)
	}
	ca := readCertificate("ca.crt")
	leaf := readCertificate("gizway-global.crt")
	wantNotBefore := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	wantNotAfter := time.Date(2120, 1, 1, 0, 0, 0, 0, time.UTC)
	if !leaf.NotBefore.Equal(wantNotBefore) || !leaf.NotAfter.Equal(wantNotAfter) {
		t.Fatalf("generated certificate validity = %s..%s, want %s..%s", leaf.NotBefore, leaf.NotAfter, wantNotBefore, wantNotAfter)
	}
	if leaf.NotBefore.After(fixtureTime) || !leaf.NotAfter.After(fixtureTime) {
		t.Fatalf("generated certificate validity %s..%s does not cover fixture time %s", leaf.NotBefore, leaf.NotAfter, fixtureTime)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots: roots, CurrentTime: fixtureTime, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("verify generated Gateway certificate at fixture time: %v", err)
	}

	dsn := initializedServiceDatabase(t, storage.OpenGizPayPostgreSQL)
	database, err := storage.OpenGizPayPostgreSQL(dsn, false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return fixtureTime })
	if err := registerNode(repository, "global", filepath.Join(pki, "gizway-global.crt")); err != nil {
		t.Fatalf("register and activate generated Gateway certificate: %v", err)
	}
	identity, err := repository.AuthenticateGatewayNode(t.Context(), leaf)
	if err != nil {
		t.Fatalf("authenticate generated Gateway certificate: %v", err)
	}
	if identity.NodeID != "gw-global-e2e" || identity.Region != "global" {
		t.Fatalf("authenticated identity = %+v", identity)
	}
}

func TestRunRejectsIncompleteBootstrapConfiguration(t *testing.T) {
	if err := run("central", "", t.TempDir(), ""); err == nil {
		t.Fatal("expected missing DSN error")
	}
	dsn := initializedServiceDatabase(t, storage.OpenGizWayPostgreSQL)
	if err := run("cn", dsn, "", ""); err == nil {
		t.Fatal("expected missing regional provider URL error")
	}
}

func TestCentralNodesModeRegistersOnlyGatewayIdentities(t *testing.T) {
	dsn := initializedServiceDatabase(t, storage.OpenGizPayPostgreSQL)
	pki := t.TempDir()
	writeGatewayCertificate(t, pki, "cn", 11)
	writeGatewayCertificate(t, pki, "global", 12)
	if err := run("central-nodes", dsn, pki, ""); err != nil {
		t.Fatal(err)
	}
	database, err := storage.OpenGizPayPostgreSQL(dsn, false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var nodes, users int
	if err := database.SQL.Get(&nodes, `SELECT count(*) FROM gateway_nodes`); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL.Get(&users, `SELECT count(*) FROM users`); err != nil {
		t.Fatal(err)
	}
	if nodes != 2 || users != 0 {
		t.Fatalf("nodes=%d users=%d", nodes, users)
	}
	regionalDSN := initializedServiceDatabase(t, storage.OpenGizWayPostgreSQL)
	if err := run("unknown", regionalDSN, pki, ""); err == nil {
		t.Fatal("unknown bootstrap mode succeeded")
	}
}

func initializedServiceDatabase(t *testing.T, open func(string, bool) (*storage.Storage, error)) string {
	t.Helper()
	dsn := testdb.NewSchema(t)
	database, err := open(dsn, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return dsn
}

func writeGatewayCertificate(t *testing.T, directory, region string, serial int64) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	san, err := url.Parse("spiffe://gizway/gateway/" + region + "/gw-" + region + "-e2e")
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "gw-" + region + "-e2e"},
		NotBefore: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		NotAfter:  time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
		URIs:      []*url.URL{san},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(directory, "gizway-"+region+".crt"), certificate, 0o600); err != nil {
		t.Fatal(err)
	}
}
