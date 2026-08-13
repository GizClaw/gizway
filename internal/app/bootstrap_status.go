package app

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// localBootstrapStatus uses aggregate existence checks only. It never calls a
// remote service, decrypts a Provider Key, or walks the Bifrost key pool.
func localBootstrapStatus(ctx context.Context, database *sqlx.DB, kind ProcessKind, bifrostSchema string) (string, error) {
	var complete bool
	if kind == ProcessGizPay {
		err := database.GetContext(ctx, &complete, `SELECT
			EXISTS(SELECT 1 FROM products p JOIN merchants m ON m.id=p.merchant_id WHERE p.status='active' AND p.billing_mode='pay_as_you_go' AND m.status='active')
			AND EXISTS(SELECT 1 FROM service_principals WHERE status='active')`)
		if err != nil {
			return "", err
		}
	} else {
		if !validSchemaName(bifrostSchema) {
			return "", fmt.Errorf("invalid Bifrost Config Store schema %q", bifrostSchema)
		}
		query := `SELECT
			EXISTS(SELECT 1 FROM models WHERE status='active')
			AND EXISTS(SELECT 1 FROM model_customer_prices)
			AND EXISTS(
				SELECT 1 FROM provider_key_billing b
				JOIN ` + quoteSQLIdentifier(bifrostSchema) + `.config_keys k ON k.key_id=b.bifrost_key_id
				WHERE b.status='active' AND length(trim(b.beneficiary_merchant_id))>0 AND k.enabled IS TRUE AND k.status='active'
			)
			AND EXISTS(SELECT 1 FROM provider_key_prices)`
		if err := database.GetContext(ctx, &complete, query); err != nil {
			return "", err
		}
	}
	if complete {
		return "complete", nil
	}
	return "incomplete", nil
}

func validSchemaName(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func quoteSQLIdentifier(value string) string { return `"` + value + `"` }
