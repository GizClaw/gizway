// Command bootstrap prepares deterministic identities and product fixtures for
// the disposable three-database E2E environment. The central-nodes mode is
// intentionally narrower: API story tests already own their account fixtures,
// so it registers only the mTLS Gateway identities required by Internal APIs.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

const (
	fixtureAt             = "2026-08-12T12:00:00.000000000Z"
	fixtureKey            = "giz_e2e_customer_key"
	fixtureUserSession    = "gzs_e2e_customer_session"
	fixtureSecondSession  = "gzs_e2e_customer_two_session"
	fixtureSecondKey      = "giz_e2e_customer_two_key"
	fixtureSecondUserID   = "e2e-user-two"
	fixtureSecondAccount  = "e2e-account-two"
	fixtureMerchant       = "e2e-merchant"
	fixtureMerchantLedger = "e2e-merchant-ledger"
)

func main() {
	mode := flag.String("mode", "", "central, central-nodes, cn, or global")
	dsn := flag.String("postgres-dsn", "", "initialized service database")
	pki := flag.String("pki-dir", "/pki", "generated certificate directory")
	providerURL := flag.String("provider-url", "", "regional fake provider URL")
	flag.Parse()
	if err := run(*mode, *dsn, *pki, *providerURL); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(mode, dsn, pki, providerURL string) error {
	if dsn == "" {
		return errors.New("postgres DSN is required")
	}
	if err := waitForSchema(dsn, mode); err != nil {
		return err
	}
	if mode == "central" {
		return bootstrapCentral(dsn, pki)
	}
	if mode == "central-nodes" {
		return bootstrapCentralNodes(dsn, pki)
	}
	if (mode == "cn" || mode == "global") && providerURL != "" {
		return bootstrapRegion(dsn, mode, providerURL)
	}
	return errors.New("mode must be central, central-nodes, cn, or global; regional mode requires provider URL")
}

func bootstrapCentralNodes(dsn, pki string) error {
	database, err := storage.OpenGizPayPostgreSQL(dsn, false)
	if err != nil {
		return err
	}
	defer database.Close()
	repository := store.New(database.SQL)
	for _, region := range []string{"cn", "global"} {
		if err := registerNode(repository, region, pki+"/gizway-"+region+".crt"); err != nil {
			return fmt.Errorf("register %s node: %w", region, err)
		}
	}
	return nil
}

func waitForSchema(dsn, mode string) error {
	service := "gizway"
	if mode == "central" || mode == "central-nodes" {
		service = "gizpay"
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		database, err := storage.OpenExistingPostgreSQL(dsn)
		if err == nil {
			var ready bool
			err = database.SQL.Get(&ready, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE service=$1 AND version=1)`, service)
			_ = database.Close()
			if err == nil && ready {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("%s schema did not become ready", service)
}

func bootstrapCentral(dsn, pki string) error {
	database, err := storage.OpenGizPayPostgreSQL(dsn, false)
	if err != nil {
		return err
	}
	defer database.Close()
	repository := store.New(database.SQL)
	var administratorCount int
	if err := database.SQL.Get(&administratorCount, `SELECT COUNT(*) FROM administrators`); err != nil {
		return fmt.Errorf("count central administrators: %w", err)
	}
	if administratorCount == 0 {
		if _, _, err := repository.BootstrapAdministrator(context.Background(), "admin@e2e.invalid", "E2E Administrator", "e2e-password", fixtureAt); err != nil {
			return fmt.Errorf("bootstrap central administrator: %w", err)
		}
	}
	for _, region := range []string{"cn", "global"} {
		if err := registerNode(repository, region, pki+"/gizway-"+region+".crt"); err != nil {
			return fmt.Errorf("register %s node: %w", region, err)
		}
	}
	ctx := context.Background()
	if _, err := database.SQL.Exec(`INSERT INTO users(id,email,display_name,status,created_at,updated_at)
		VALUES ('e2e-user','customer@e2e.invalid','E2E Customer','active',$1,$1)
		ON CONFLICT (id) DO NOTHING`, fixtureAt); err != nil {
		return err
	}
	if _, err := database.SQL.Exec(`INSERT INTO accounts(id,owner_user_id,kind,name,status,created_at,updated_at)
		VALUES ('e2e-account','e2e-user','personal','E2E Customer','active',$1,$1)
		ON CONFLICT (id) DO NOTHING`, fixtureAt); err != nil {
		return err
	}
	if _, err := database.SQL.Exec(`INSERT INTO ledger_accounts(id,owner_account_id,code,kind,asset_code,normal_balance,status,created_at,updated_at)
		VALUES ('e2e-ledger','e2e-account','USER:e2e-account','user_credit','GIZ_CREDIT','credit','active',$1,$1)
		ON CONFLICT (code) DO NOTHING`, fixtureAt); err != nil {
		return err
	}
	secretHash := sha256.Sum256([]byte(fixtureKey))
	if _, _, err := repository.CreateAPIKey(ctx, "e2e-user", "e2e-key", []byte("e2e-key"), secretHash[:], store.APIKey{
		ID: "e2e-key", AccountID: "e2e-account", Kind: "gateway", Name: "E2E Gateway",
		KeyPrefix: fixtureKey[:12], Scopes: store.JSON(`["account:self","gateway:invoke","gateway:usage:read"]`), CreatedAt: fixtureAt,
	}); err != nil && !errors.Is(err, store.ErrIdempotencyConflict) {
		return err
	}
	if err := bootstrapProductIdentities(database, repository); err != nil {
		return err
	}
	checkout := "https://checkout.e2e.invalid/topup"
	topup := store.Topup{
		ID: "e2e-topup", AccountID: "e2e-account", PaymentProvider: "e2e", ProviderReference: "e2e-provider-topup",
		FiatCurrency: "USD", FiatAmountMinor: 100, Rate: store.TopupRateSnapshot{
			Base:      store.TopupRate{FiatMinor: 100, CreditMicrocredits: 100_000_000},
			Effective: store.TopupRate{FiatMinor: 100, CreditMicrocredits: 100_000_000},
		}, CreditAmount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 100_000_000},
		Status: "pending", CheckoutURL: &checkout, CreatedAt: fixtureAt,
	}
	if _, _, err := repository.CreateTopup(ctx, "e2e-user", "e2e-topup", []byte("e2e-topup"), topup); err != nil && !errors.Is(err, store.ErrIdempotencyConflict) {
		return err
	}
	_, _, err = repository.CompleteTopupEvent(ctx, "e2e-topup-event", topup.ProviderReference, "USD", 100, []byte("e2e-topup-event"), fixtureAt)
	return err
}

func bootstrapProductIdentities(database *storage.Storage, repository *store.Store) error {
	ctx := context.Background()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,email,display_name,status,created_at,updated_at)
		 VALUES ($1,'customer-two@e2e.invalid','E2E Customer Two','active',$2,$2) ON CONFLICT (id) DO NOTHING`, []any{fixtureSecondUserID, fixtureAt}},
		{`INSERT INTO accounts(id,owner_user_id,kind,name,status,created_at,updated_at)
		 VALUES ($1,$2,'personal','E2E Customer Two','active',$3,$3) ON CONFLICT (id) DO NOTHING`, []any{fixtureSecondAccount, fixtureSecondUserID, fixtureAt}},
		{`INSERT INTO accounts(id,owner_user_id,kind,name,status,created_at,updated_at)
		 VALUES ($1,'e2e-user','merchant','E2E Merchant','active',$2,$2) ON CONFLICT (id) DO NOTHING`, []any{fixtureMerchant, fixtureAt}},
		{`INSERT INTO merchant_accounts(account_id,owner_user_id,legal_name,public_name,review_level,merchant_status,country_code,created_at,updated_at)
		 VALUES ($1,'e2e-user','E2E Merchant LLC','E2E Merchant','enhanced','approved','US',$2,$2) ON CONFLICT (account_id) DO NOTHING`, []any{fixtureMerchant, fixtureAt}},
		{`INSERT INTO ledger_accounts(id,owner_account_id,code,kind,asset_code,normal_balance,status,created_at,updated_at)
		 VALUES ('e2e-ledger-two',$1,'USER:e2e-account-two','user_credit','GIZ_CREDIT','credit','active',$2,$2),
		        ($3,$4,'MERCHANT:e2e-merchant','merchant_credit','GIZ_CREDIT','credit','active',$2,$2)
		 ON CONFLICT (id) DO NOTHING`, []any{fixtureSecondAccount, fixtureAt, fixtureMerchantLedger, fixtureMerchant}},
	}
	for _, statement := range statements {
		if _, err := database.SQL.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}
	for _, session := range []struct{ id, userID, raw string }{
		{"e2e-session", "e2e-user", fixtureUserSession},
		{"e2e-session-two", fixtureSecondUserID, fixtureSecondSession},
	} {
		digest := sha256.Sum256([]byte(session.raw))
		if _, err := database.SQL.ExecContext(ctx, `INSERT INTO user_sessions(id,user_id,secret_hash,status,expires_at,created_at)
			VALUES ($1,$2,$3,'active','2027-08-12T00:00:00.000000000Z',$4) ON CONFLICT (id) DO NOTHING`,
			session.id, session.userID, digest[:], fixtureAt); err != nil {
			return err
		}
	}
	secondKeyHash := sha256.Sum256([]byte(fixtureSecondKey))
	if _, _, err := repository.CreateAPIKey(ctx, fixtureSecondUserID, "e2e-key-two", []byte("e2e-key-two"), secondKeyHash[:], store.APIKey{
		ID: "e2e-key-two", AccountID: fixtureSecondAccount, Kind: "gateway", Name: "E2E Gateway Two",
		KeyPrefix: "giz_e2e_two_", Scopes: store.JSON(`["account:self","gateway:invoke","gateway:usage:read"]`), CreatedAt: fixtureAt,
	}); err != nil && !errors.Is(err, store.ErrIdempotencyConflict) {
		return err
	}
	return nil
}

func registerNode(repository *store.Store, region, certificatePath string) error {
	certificatePEM, err := os.ReadFile(certificatePath)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(certificatePEM)
	if block == nil {
		return fmt.Errorf("decode %s", certificatePath)
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	nodeID := "gw-" + region + "-e2e"
	if _, _, err := repository.CreateGatewayNode(context.Background(), nodeID, region, strings.ToUpper(region)+" E2E", fixtureAt); err != nil && !errors.Is(err, store.ErrIdempotencyConflict) {
		return fmt.Errorf("create Gateway node: %w", err)
	} else if errors.Is(err, store.ErrIdempotencyConflict) {
		return fmt.Errorf("existing Gateway node metadata differs: %w", err)
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	san := ""
	if len(certificate.URIs) != 0 {
		san = certificate.URIs[0].String()
	}
	registered, _, err := repository.RegisterGatewayNodeCertificate(context.Background(), nodeID, hex.EncodeToString(fingerprint[:]),
		certificate.SerialNumber.String(), certificate.Subject.String(), san, timetext.Format(certificate.NotBefore), timetext.Format(certificate.NotAfter), fixtureAt)
	if err != nil {
		return fmt.Errorf("register Gateway certificate: %w", err)
	}
	_, err = repository.ActivateGatewayNodeCertificate(context.Background(), nodeID, registered.ID, fixtureAt)
	if err != nil {
		return fmt.Errorf("activate Gateway certificate: %w", err)
	}
	return nil
}

func bootstrapRegion(dsn, region, providerURL string) error {
	database, err := storage.OpenGizWayPostgreSQL(dsn, false)
	if err != nil {
		return err
	}
	defer database.Close()
	repository, err := store.NewWithSecretKey(database.SQL, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		return err
	}
	repository.ConfigureClock(func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) })
	administrator, _, err := repository.BootstrapRegionalAdministrator(context.Background(), region+"-admin@e2e.invalid", strings.ToUpper(region)+" Operator", "e2e-password", fixtureAt)
	if err != nil {
		return err
	}
	provider, err := repository.CreateProvider(context.Background(), administrator.ID, region+"-provider", strings.ToUpper(region)+" Provider", fixtureAt)
	if err != nil {
		return err
	}
	endpoint, err := repository.CreateProviderEndpoint(context.Background(), administrator.ID, provider.ID, "primary", providerURL, "story-provider-key", &region, 1, 100, fixtureAt)
	if err != nil {
		return err
	}
	model, err := repository.CreateModel(context.Background(), administrator.ID, store.Model{Slug: region + "-model", Name: strings.ToUpper(region) + " Model", Modality: store.JSON(`["text"]`)})
	if err != nil {
		return err
	}
	contextWindow, maxOutput := int64(8192), int64(2048)
	variant, err := repository.CreateModelVariant(context.Background(), administrator.ID, store.ModelVariant{
		ModelID: model.ID, ProviderEndpointID: endpoint.ID, ProviderModelName: "story-text", VariantSlug: "primary",
		Capabilities: store.JSON(`{"responses":true,"chat":true}`), ContextWindow: &contextWindow, MaxOutputTokens: &maxOutput,
	})
	if err != nil {
		return err
	}
	for _, metric := range []string{"input_token", "cached_input_token", "output_token"} {
		if _, err := repository.CreateModelPrice(context.Background(), administrator.ID, store.ModelPrice{
			ModelVariantID: variant.ID, Metric: metric, UnitSize: 1000,
			UpstreamCostMicrocredits: 1, BaseCustomerPriceMicrocredits: 10, CustomerPriceMicrocredits: 10,
			ValidFrom: "2026-01-01T00:00:00.000000000Z",
		}); err != nil {
			return err
		}
	}
	return nil
}
