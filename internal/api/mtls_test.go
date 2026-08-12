package api_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/idy/gizway/internal/api"
	gizpayclient "github.com/idy/gizway/internal/client/gizpay"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

// This is the transport-level proof that Internal API identity comes from a
// CA-verified client certificate and the GizPay fingerprint registry. Headers
// and request JSON cannot manufacture node_id or region.
func TestPostgreSQLGizPayMTLSRejectsMissingAndRevokedNodeCertificates(t *testing.T) {
	database := testdb.OpenGizPay(t)
	defer database.Close()
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return now })
	administrator, _, err := repository.BootstrapAdministrator(t.Context(), "mtls-admin@test.invalid", "mTLS Admin", "test-password", "2026-08-11T00:00:00.000000000Z")
	if err != nil {
		t.Fatal(err)
	}
	adminSecret := "gizadm_mtls_test"
	adminHash := sha256.Sum256([]byte(adminSecret))
	if _, _, err := repository.CreateAdminAPIKey(t.Context(), administrator.ID, "mtls-admin-key", []byte("mtls-admin-key"), adminHash[:], store.AdminAPIKey{
		ID: "mtls-admin-key", AdministratorID: administrator.ID, Name: "mTLS test", KeyPrefix: "gizadm_mtls", Status: "active", CreatedAt: "2026-08-11T00:00:00.000000000Z",
	}); err != nil {
		t.Fatal(err)
	}
	ca, caKey, caPEM := issueTestCA(t)
	serverCertificate, _ := issueTestCertificate(t, ca, caKey, true, nil)
	san, _ := url.Parse("spiffe://gizway/gateway/global/mtls-node")
	clientCertificate, leaf := issueTestCertificate(t, ca, caKey, false, san)
	fingerprint := sha256.Sum256(leaf.Raw)
	if _, _, err := repository.CreateGatewayNode(t.Context(), "mtls-node", "global", "mTLS node", "2026-08-11T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	registered, _, err := repository.RegisterGatewayNodeCertificate(t.Context(), "mtls-node", hex.EncodeToString(fingerprint[:]),
		leaf.SerialNumber.String(), leaf.Subject.String(), san.String(), "2026-08-11T00:00:00.000000000Z", "2026-08-13T00:00:00.000000000Z", "2026-08-11T00:00:00.000000000Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ActivateGatewayNodeCertificate(t.Context(), "mtls-node", registered.ID, "2026-08-12T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}

	apiServer := api.NewWithServicesAndClockSurface(repository, nil, nil, nil, func() time.Time { return now }, nil, api.SurfaceGizPay)
	request := httptest.NewRequest(http.MethodPost, "/admin/v1/gateway_nodes/mtls-node/certificates", bytes.NewBufferString(`{
		"fingerprint_sha256":"2222222222222222222222222222222222222222222222222222222222222222",
		"serial_number":"handler-serial","subject_dn":"CN=handler-node",
		"san_uri":"spiffe://gizway/gateway/global/handler-node",
		"not_before":"2026-08-11T00:00:00.000000000Z","not_after":"2026-08-13T00:00:00.000000000Z"}`))
	request.Header.Set("Authorization", "Bearer "+adminSecret)
	request.Header.Set("Idempotency-Key", "handler-register-certificate")
	recorder := httptest.NewRecorder()
	apiServer.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("certificate registration status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	tlsServer := httptest.NewUnstartedServer(apiServer.Handler())
	clientRoots := x509.NewCertPool()
	clientRoots.AppendCertsFromPEM(caPEM)
	tlsServer.TLS = &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCertificate},
		ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: clientRoots,
	}
	tlsServer.StartTLS()
	defer tlsServer.Close()

	serverRoots := x509.NewCertPool()
	serverRoots.AppendCertsFromPEM(caPEM)
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: serverRoots, Certificates: []tls.Certificate{clientCertificate},
	}}}
	client, err := gizpayclient.New(tlsServer.URL, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckReadiness(t.Context(), "mtls-node", "global"); err != nil {
		t.Fatalf("matching readiness identity: %v", err)
	}
	if err := client.CheckReadiness(t.Context(), "mtls-node", "cn"); !errors.Is(err, gizpayclient.ErrInvalidNodeIdentity) {
		t.Fatalf("mismatched readiness identity error=%v", err)
	}

	withoutCertificate, err := gizpayclient.New(tlsServer.URL, tlsServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withoutCertificate.Exchange(t.Context(), "giz_story_user_active_1", nil); err != gizpayclient.ErrInvalidNodeIdentity {
		t.Fatalf("missing certificate error=%v", err)
	}
	if _, err := repository.RevokeGatewayNodeCertificate(t.Context(), "mtls-node", registered.ID, "2026-08-12T00:00:01.000000000Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Exchange(t.Context(), "giz_story_user_active_1", nil); err != gizpayclient.ErrInvalidNodeIdentity {
		t.Fatalf("revoked certificate error=%v", err)
	}
}

func issueTestCA(t *testing.T) (*x509.Certificate, *rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Gizway test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func issueTestCertificate(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey, server bool, san *url.URL) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "mtls-node"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
	}
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		template.URIs = []*url.URL{san}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.Raw}, PrivateKey: key, Leaf: leaf}, leaf
}
