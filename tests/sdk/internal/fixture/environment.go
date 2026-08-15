package fixture

import (
	"os"
	"testing"
)

type Environment struct {
	GlobalURL          string
	CNURL              string
	PayURL             string
	GlobalDSN          string
	PayDSN             string
	ProviderURL        string
	ProviderKeyID      string
	SubscriptionKey    string
	RevokedKey         string
	GlobalModel        string
	CNModel            string
	InactiveModel      string
	ProviderErrorModel string
	AccountID          string
	HumanToken         string
	ZeroPriceModel     string
}

func Load(t *testing.T) Environment {
	t.Helper()
	env := Environment{
		GlobalURL: os.Getenv("M03_GLOBAL_URL"), CNURL: os.Getenv("M03_CN_URL"),
		PayURL: os.Getenv("M03_PAY_URL"), SubscriptionKey: os.Getenv("M03_SUBSCRIPTION_KEY"),
		GlobalDSN: os.Getenv("M03_GLOBAL_DSN"), PayDSN: os.Getenv("M03_PAY_DSN"),
		ProviderURL: os.Getenv("M03_PROVIDER_URL"), ProviderKeyID: os.Getenv("M03_PROVIDER_KEY_ID"),
		RevokedKey:  os.Getenv("M03_REVOKED_SUBSCRIPTION_KEY"),
		GlobalModel: os.Getenv("M03_GLOBAL_MODEL"), CNModel: os.Getenv("M03_CN_MODEL"),
		InactiveModel: os.Getenv("M03_INACTIVE_MODEL"), ProviderErrorModel: os.Getenv("M03_PROVIDER_ERROR_MODEL"),
		AccountID: os.Getenv("M03_ACCOUNT_ID"), HumanToken: os.Getenv("M03_HUMAN_TOKEN"),
		ZeroPriceModel: os.Getenv("M03_ZERO_PRICE_MODEL"),
	}
	if env.GlobalURL == "" || env.SubscriptionKey == "" || env.GlobalModel == "" {
		t.Skip("Milestone 03 SDK E2E environment is not configured")
	}
	return env
}
