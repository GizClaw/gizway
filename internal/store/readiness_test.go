package store_test

import (
	"testing"
	"time"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

func TestGizPayReadinessTracksOnlyOwnedBootstrapState(t *testing.T) {
	database := testdb.OpenGizPay(t)
	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) })
	checks, err := repository.GizPayReadinessChecks(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if checks["bootstrap_admin"] != "pending" || checks["usage_billing"] != "pending" || checks["node_registry"] != "pending" {
		t.Fatalf("fresh readiness: %+v", checks)
	}
	const at = "2026-08-12T00:00:00.000000000Z"
	if _, _, err := repository.BootstrapAdministrator(t.Context(), "operator@gizpay.invalid", "Operator", "password", at); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO gateway_nodes(id,region,name,created_at,updated_at)
		VALUES ('gw-cn-ready','cn','CN Ready',$1,$1)`, at); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO gateway_node_certificates
		(id,node_id,fingerprint_sha256,serial_number,subject_dn,san_uri,status,not_before,not_after,created_at)
		VALUES ('cert-ready','gw-cn-ready',$1,'1','CN=gw-cn-ready','spiffe://gizway/gateway/cn/gw-cn-ready','active',
		'2026-08-11T00:00:00.000000000Z','2026-08-13T00:00:00.000000000Z',$2)`, make([]byte, 32), at); err != nil {
		t.Fatal(err)
	}
	checks, err = repository.GizPayReadinessChecks(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"database", "schema", "bootstrap_admin", "usage_billing", "quota_calculator", "node_registry"} {
		if checks[name] != "ready" {
			t.Fatalf("check %s=%q in %+v", name, checks[name], checks)
		}
	}
}
