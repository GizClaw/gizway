package store

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/idy/gizway/internal/timetext"
)

type GatewayNodeIdentity struct {
	NodeID string `db:"node_id"`
	Region string `db:"region"`
}

type GatewayNode struct {
	ID           string                   `db:"id" json:"id"`
	Region       string                   `db:"region" json:"region"`
	Name         string                   `db:"name" json:"name"`
	CreatedAt    string                   `db:"created_at" json:"created_at"`
	UpdatedAt    string                   `db:"updated_at" json:"updated_at"`
	Certificates []GatewayNodeCertificate `json:"certificates"`
}

type GatewayNodeCertificate struct {
	ID          string  `db:"id" json:"id"`
	NodeID      string  `db:"node_id" json:"node_id"`
	Fingerprint []byte  `db:"fingerprint_sha256" json:"-"`
	Serial      string  `db:"serial_number" json:"serial_number"`
	SubjectDN   string  `db:"subject_dn" json:"subject_dn"`
	SANURI      string  `db:"san_uri" json:"san_uri"`
	Status      string  `db:"status" json:"status"`
	NotBefore   string  `db:"not_before" json:"not_before"`
	NotAfter    string  `db:"not_after" json:"not_after"`
	CreatedAt   string  `db:"created_at" json:"created_at"`
	RevokedAt   *string `db:"revoked_at" json:"revoked_at,omitempty"`
}

func (certificate GatewayNodeCertificate) MarshalJSON() ([]byte, error) {
	type alias GatewayNodeCertificate
	return json.Marshal(struct {
		alias
		FingerprintSHA256 string `json:"fingerprint_sha256"`
	}{alias: alias(certificate), FingerprintSHA256: hex.EncodeToString(certificate.Fingerprint)})
}

// CreateGatewayNode registers stable deployment identity only. There is no
// product-level node enable/disable state; access is controlled by active
// certificate rows and the deployment itself.
func (s *Store) CreateGatewayNode(ctx context.Context, id, region, name, at string) (GatewayNode, bool, error) {
	id, name = strings.TrimSpace(id), strings.TrimSpace(name)
	if id == "" || name == "" || (region != "cn" && region != "global") {
		return GatewayNode{}, false, errors.New("node id, cn/global region, and name are required")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return GatewayNode{}, false, err
	}
	defer tx.Rollback()
	var existing GatewayNode
	err = tx.GetContext(ctx, &existing, `SELECT id,region,name,created_at,updated_at FROM gateway_nodes WHERE id=?`, id)
	if err == nil {
		if existing.Region != region || existing.Name != name {
			return GatewayNode{}, false, ErrIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return GatewayNode{}, false, err
		}
		withCertificates, err := s.GetGatewayNode(ctx, id)
		return withCertificates, true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return GatewayNode{}, false, err
	}
	node := GatewayNode{ID: id, Region: region, Name: name, CreatedAt: at, UpdatedAt: at, Certificates: []GatewayNodeCertificate{}}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gateway_nodes(id,region,name,created_at,updated_at) VALUES (?,?,?,?,?)`, id, region, name, at, at); err != nil {
		return GatewayNode{}, false, err
	}
	if err := recordGatewayNodeAudit(ctx, tx, "gateway_node.created", "gateway_node", id, at); err != nil {
		return GatewayNode{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return GatewayNode{}, false, err
	}
	return node, false, nil
}

func (s *Store) RegisterGatewayNodeCertificate(ctx context.Context, nodeID, fingerprintHex, serial, subjectDN, sanURI, notBefore, notAfter, at string) (GatewayNodeCertificate, bool, error) {
	fingerprint, err := hex.DecodeString(strings.TrimSpace(fingerprintHex))
	if err != nil || len(fingerprint) != sha256.Size || strings.TrimSpace(serial) == "" || strings.TrimSpace(subjectDN) == "" || strings.TrimSpace(sanURI) == "" {
		return GatewayNodeCertificate{}, false, errors.New("valid SHA-256 fingerprint, serial, subject DN, and SAN URI are required")
	}
	notBefore, err = timetext.Normalize(notBefore)
	if err != nil {
		return GatewayNodeCertificate{}, false, errors.New("certificate not_before is invalid")
	}
	notAfter, err = timetext.Normalize(notAfter)
	if err != nil {
		return GatewayNodeCertificate{}, false, errors.New("certificate not_after is invalid")
	}
	before, _ := timetext.Parse(notBefore)
	after, err := timetext.Parse(notAfter)
	if err != nil || !after.After(before) {
		return GatewayNodeCertificate{}, false, errors.New("certificate not_after must be after not_before")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return GatewayNodeCertificate{}, false, err
	}
	defer tx.Rollback()
	var existing GatewayNodeCertificate
	err = tx.GetContext(ctx, &existing, `SELECT id,node_id,fingerprint_sha256,serial_number,subject_dn,san_uri,status,not_before,not_after,created_at,revoked_at
		FROM gateway_node_certificates WHERE fingerprint_sha256=?`, fingerprint)
	if err == nil {
		if existing.NodeID != nodeID || existing.Serial != serial || existing.SubjectDN != subjectDN || existing.SANURI != sanURI || existing.NotBefore != notBefore || existing.NotAfter != notAfter {
			return GatewayNodeCertificate{}, false, ErrIdempotencyConflict
		}
		return existing, true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return GatewayNodeCertificate{}, false, err
	}
	certificate := GatewayNodeCertificate{
		ID: uuid.NewString(), NodeID: nodeID, Fingerprint: fingerprint, Serial: serial,
		SubjectDN: subjectDN, SANURI: sanURI, Status: "pending",
		NotBefore: notBefore, NotAfter: notAfter, CreatedAt: at,
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gateway_node_certificates
		(id,node_id,fingerprint_sha256,serial_number,subject_dn,san_uri,status,not_before,not_after,created_at)
		VALUES (?,?,?,?,?,?,'pending',?,?,?)`, certificate.ID, nodeID, fingerprint, serial, subjectDN, sanURI, notBefore, notAfter, at); err != nil {
		return GatewayNodeCertificate{}, false, err
	}
	if err := recordGatewayNodeAudit(ctx, tx, "gateway_node_certificate.registered", "gateway_node_certificate", certificate.ID, at); err != nil {
		return GatewayNodeCertificate{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return GatewayNodeCertificate{}, false, err
	}
	return certificate, false, nil
}

func (s *Store) ActivateGatewayNodeCertificate(ctx context.Context, nodeID, certificateID, at string) (GatewayNodeCertificate, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return GatewayNodeCertificate{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE gateway_node_certificates SET status='active'
		WHERE id=? AND node_id=? AND status IN ('pending','active') AND not_before<=? AND not_after>?`, certificateID, nodeID, at, at)
	if err != nil {
		return GatewayNodeCertificate{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return GatewayNodeCertificate{}, ErrNotFound
	}
	if err := recordGatewayNodeAudit(ctx, tx, "gateway_node_certificate.activated", "gateway_node_certificate", certificateID, at); err != nil {
		return GatewayNodeCertificate{}, err
	}
	if err := tx.Commit(); err != nil {
		return GatewayNodeCertificate{}, err
	}
	return s.getGatewayNodeCertificate(ctx, nodeID, certificateID)
}

func (s *Store) RevokeGatewayNodeCertificate(ctx context.Context, nodeID, certificateID, at string) (GatewayNodeCertificate, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return GatewayNodeCertificate{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE gateway_node_certificates SET status='revoked',revoked_at=?
		WHERE id=? AND node_id=? AND status IN ('pending','active')`, at, certificateID, nodeID)
	if err != nil {
		return GatewayNodeCertificate{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return GatewayNodeCertificate{}, ErrNotFound
	}
	if err := recordGatewayNodeAudit(ctx, tx, "gateway_node_certificate.revoked", "gateway_node_certificate", certificateID, at); err != nil {
		return GatewayNodeCertificate{}, err
	}
	if err := tx.Commit(); err != nil {
		return GatewayNodeCertificate{}, err
	}
	return s.getGatewayNodeCertificate(ctx, nodeID, certificateID)
}

func recordGatewayNodeAudit(ctx context.Context, tx *boundTx, action, resourceType, resourceID, at string) error {
	if actor, ok := authenticatedAuditActor(ctx); ok {
		return recordAudit(ctx, tx, actor.Type, actor.ID, action, resourceType, resourceID, "", "{}", at)
	}
	// Direct Store callers are bootstrap/deployment automation. The audit table
	// intentionally has no actor foreign key, so this remains truthful without
	// manufacturing an administrator identity in a fresh control-plane database.
	return recordAudit(ctx, tx, "system", "bootstrap", action, resourceType, resourceID, "", "{}", at)
}

func (s *Store) GetGatewayNode(ctx context.Context, id string) (GatewayNode, error) {
	var node GatewayNode
	if err := s.db.GetContext(ctx, &node, `SELECT id,region,name,created_at,updated_at FROM gateway_nodes WHERE id=?`, id); errors.Is(err, sql.ErrNoRows) {
		return GatewayNode{}, ErrNotFound
	} else if err != nil {
		return GatewayNode{}, err
	}
	if err := s.db.SelectContext(ctx, &node.Certificates, `SELECT id,node_id,fingerprint_sha256,serial_number,subject_dn,san_uri,status,not_before,not_after,created_at,revoked_at
		FROM gateway_node_certificates WHERE node_id=? ORDER BY created_at,id`, id); err != nil {
		return GatewayNode{}, err
	}
	return node, nil
}

func (s *Store) getGatewayNodeCertificate(ctx context.Context, nodeID, id string) (GatewayNodeCertificate, error) {
	var certificate GatewayNodeCertificate
	if err := s.db.GetContext(ctx, &certificate, `SELECT id,node_id,fingerprint_sha256,serial_number,subject_dn,san_uri,status,not_before,not_after,created_at,revoked_at
		FROM gateway_node_certificates WHERE node_id=? AND id=?`, nodeID, id); errors.Is(err, sql.ErrNoRows) {
		return GatewayNodeCertificate{}, ErrNotFound
	} else if err != nil {
		return GatewayNodeCertificate{}, err
	}
	return certificate, nil
}

// AuthenticateGatewayNode binds a TLS-verified leaf certificate to the
// center's registered fingerprint and expected subject. Rotation is represented
// by overlapping active rows; revocation removes only the selected leaf.
func (s *Store) AuthenticateGatewayNode(ctx context.Context, certificate *x509.Certificate) (GatewayNodeIdentity, error) {
	if certificate == nil {
		return GatewayNodeIdentity{}, ErrNotFound
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	var row struct {
		NodeID    string `db:"node_id"`
		Region    string `db:"region"`
		SubjectDN string `db:"subject_dn"`
		SANURI    string `db:"san_uri"`
	}
	err := s.db.GetContext(ctx, &row, `SELECT c.node_id,n.region,c.subject_dn,c.san_uri
		FROM gateway_node_certificates c JOIN gateway_nodes n ON n.id=c.node_id
		WHERE c.fingerprint_sha256=? AND c.status='active'
		  AND c.not_before<=? AND c.not_after>?`, fingerprint[:], timetext.Format(s.now()), timetext.Format(s.now()))
	if errors.Is(err, sql.ErrNoRows) {
		return GatewayNodeIdentity{}, ErrNotFound
	}
	if err != nil {
		return GatewayNodeIdentity{}, fmt.Errorf("authenticate Gateway node certificate: %w", err)
	}
	certificateURIs := make([]string, 0, len(certificate.URIs))
	for _, uri := range certificate.URIs {
		certificateURIs = append(certificateURIs, uri.String())
	}
	if certificate.Subject.String() != row.SubjectDN || !slices.Contains(certificateURIs, row.SANURI) {
		return GatewayNodeIdentity{}, ErrNotFound
	}
	return GatewayNodeIdentity{NodeID: row.NodeID, Region: row.Region}, nil
}
