package store_test

import (
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

func TestPostgreSQLGatewayNodeIdentityRequiresFingerprintSubjectSANValidityAndActiveStatus(t *testing.T) {
	database, err := storage.OpenGizPayPostgreSQL(testdb.NewSchema(t), true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return now })
	san, _ := url.Parse("spiffe://gizway/gateway/global/node-one")
	certificate := &x509.Certificate{Raw: []byte("active-node-certificate"), Subject: pkix.Name{CommonName: "node-one"}, URIs: []*url.URL{san}}
	fingerprint := sha256.Sum256(certificate.Raw)
	if _, err := database.SQL.Exec(`INSERT INTO gateway_nodes(id,region,name,created_at,updated_at) VALUES ('node-one','global','Global one',$1,$1)`, "2026-08-11T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO gateway_node_certificates
		(id,node_id,fingerprint_sha256,serial_number,subject_dn,san_uri,status,not_before,not_after,created_at)
		VALUES ('cert-one','node-one',$1,'serial-one',$2,$3,'active',$4,$5,$4)`, fingerprint[:], certificate.Subject.String(), san.String(),
		"2026-08-11T00:00:00.000000000Z", "2026-08-13T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	identity, err := repository.AuthenticateGatewayNode(t.Context(), certificate)
	if err != nil || identity.NodeID != "node-one" || identity.Region != "global" {
		t.Fatalf("active identity = %+v, %v", identity, err)
	}
	wrongSAN, _ := url.Parse("spiffe://gizway/gateway/cn/node-one")
	wrongCertificate := *certificate
	wrongCertificate.URIs = []*url.URL{wrongSAN}
	if _, err := repository.AuthenticateGatewayNode(t.Context(), &wrongCertificate); err == nil {
		t.Fatal("certificate with registered fingerprint but wrong SAN was accepted")
	}
	if _, err := database.SQL.Exec(`UPDATE gateway_node_certificates SET status='revoked',revoked_at=$1 WHERE id='cert-one'`, "2026-08-12T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AuthenticateGatewayNode(t.Context(), certificate); err == nil {
		t.Fatal("revoked certificate was accepted")
	}
}

func TestPostgreSQLGatewayNodeLifecycleWritesAuditAtomically(t *testing.T) {
	database, err := storage.OpenGizPayPostgreSQL(testdb.NewSchema(t), true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := store.New(database.SQL)
	at := "2026-08-12T00:00:00.000000000Z"
	if _, _, err := repository.CreateGatewayNode(t.Context(), "node-audit", "cn", "CN audit node", at); err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256([]byte("audit-certificate"))
	certificate, _, err := repository.RegisterGatewayNodeCertificate(t.Context(), "node-audit", fmt.Sprintf("%x", fingerprint[:]),
		"serial-audit", "CN=node-audit", "spiffe://gizway/gateway/cn/node-audit",
		"2026-08-11T00:00:00Z", "2026-08-13T00:00:00Z", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ActivateGatewayNodeCertificate(t.Context(), "node-audit", certificate.ID, at); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RevokeGatewayNodeCertificate(t.Context(), "node-audit", certificate.ID, at); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err := database.SQL.Get(&audits, `SELECT COUNT(*) FROM audit_events WHERE actor_type='system' AND actor_id='bootstrap' AND resource_id IN ($1,$2)`, "node-audit", certificate.ID); err != nil || audits != 4 {
		t.Fatalf("node lifecycle audit count=%d err=%v, want 4", audits, err)
	}
}
