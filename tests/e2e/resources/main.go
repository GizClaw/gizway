// Command e2e-resources prepares disposable E2E resources through public and admin APIs.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const zitadelAPIScope = "urn:zitadel:iam:org:project:id:zitadel:aud"

type options struct {
	mode, zitadelURL, outputDirectory, fixtureFile string
	gizpayURL, cnURL, globalURL                    string
	identityFile, story                            string
	resourceConfigFile, hmacSecretFile             string
	cnProviderURL, globalProviderURL               string
}

func main() {
	var options options
	flag.StringVar(&options.mode, "mode", "", "zitadel or milestone-03")
	flag.StringVar(&options.zitadelURL, "zitadel-url", "", "ZITADEL base URL")
	flag.StringVar(&options.outputDirectory, "output-directory", "/fixtures", "identity fixture directory")
	flag.StringVar(&options.fixtureFile, "fixture-file", "/fixtures/e2e.vars", "Hurl variables file")
	flag.StringVar(&options.gizpayURL, "gizpay-url", "", "GizPay base URL")
	flag.StringVar(&options.cnURL, "cn-url", "", "CN GizWay base URL")
	flag.StringVar(&options.globalURL, "global-url", "", "Global GizWay base URL")
	flag.StringVar(&options.identityFile, "identity-file", "/fixtures/identity.vars", "identity variables file")
	flag.StringVar(&options.story, "story", "e2e", "isolated API story name")
	flag.StringVar(&options.resourceConfigFile, "resource-config", "", "E2E API resource YAML configuration")
	flag.StringVar(&options.hmacSecretFile, "hmac-secret-file", "", "shared HMAC Secret file")
	flag.StringVar(&options.cnProviderURL, "cn-provider-url", "", "CN fake Provider URL")
	flag.StringVar(&options.globalProviderURL, "global-provider-url", "", "Global fake Provider URL")
	flag.Parse()
	var err error
	switch options.mode {
	case "zitadel":
		err = bootstrapZITADEL(options)
	case "milestone-03":
		err = bootstrapMilestone03(options)
	default:
		err = errors.New("mode must be zitadel or milestone-03")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type machineKey struct {
	Type   string `json:"type"`
	KeyID  string `json:"keyId"`
	Key    string `json:"key"`
	UserID string `json:"userId"`
}

type zitadelClient struct {
	baseURL    string
	token      string
	loginToken string
	orgID      string
	http       *http.Client
}

func bootstrapZITADEL(options options) error {
	if options.zitadelURL == "" || options.outputDirectory == "" {
		return errors.New("zitadel-url and output-directory are required")
	}
	bootstrapKeyPath := filepath.Join(options.outputDirectory, "zitadel-bootstrap-machine.json")
	var bootstrapKey machineKey
	var err error
	deadline := time.Now().Add(90 * time.Second)
	for {
		bootstrapKey, err = readMachineKey(bootstrapKeyPath)
		if err == nil {
			break
		}
		// start-from-init writes the key file into the shared volume. It can be
		// observed between create and close, so retry parse errors as well as an
		// absent file until initialization has had a fair chance to finish.
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(time.Second)
	}
	var token string
	deadline = time.Now().Add(90 * time.Second)
	for {
		token, err = exchangeJWTBearer(context.Background(), options.zitadelURL, bootstrapKey, []string{"openid", zitadelAPIScope})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("bootstrap ZITADEL token: %w", err)
		}
		time.Sleep(time.Second)
	}
	pat, err := os.ReadFile(filepath.Join(options.outputDirectory, "zitadel-login-client.pat"))
	if err != nil {
		return fmt.Errorf("read bootstrap Human PAT: %w", err)
	}
	client := &zitadelClient{baseURL: strings.TrimRight(options.zitadelURL, "/"), token: token, loginToken: strings.TrimSpace(string(pat)), http: &http.Client{Timeout: 15 * time.Second}}
	if err := client.lookupOrgID(); err != nil {
		return err
	}
	projects := []projectFixture{
		{ID: "386000000000000001", Name: "GizPay Account", Roles: []string{"public_catalog", "public_catalog_cn", "public_catalog_global"}},
		{ID: "386000000000000002", Name: "GizPay Service", Roles: []string{"credit_check", "charge"}},
		{ID: "386000000000000003", Name: "GizWay CN Admin", Roles: []string{"administrator"}},
		{ID: "386000000000000004", Name: "GizWay Global Admin", Roles: []string{"administrator"}},
	}
	for _, project := range projects {
		if err := client.createProject(project); err != nil {
			return err
		}
	}
	if reusable, err := reusableZITADELFixtures(options.outputDirectory); err != nil {
		return err
	} else if reusable {
		return nil
	}
	applications := make([]string, len(projects))
	for index, project := range projects {
		clientID, err := client.createOIDCApplication(project.ID, project.Name+" E2E")
		if err != nil {
			return err
		}
		applications[index] = clientID
	}
	identities := []identityFixture{
		{ID: "human-primary", Human: true, Audiences: []string{projects[0].ID}},
		{ID: "human-two", Human: true, Audiences: []string{projects[0].ID}},
		{ID: "web-first-login", Human: true, Audiences: []string{projects[0].ID}},
		{ID: "provider-merchant-human", Human: true, Audiences: []string{projects[0].ID}},
		{ID: "provider-merchant-human-two", Human: true, Audiences: []string{projects[0].ID}},
		{ID: "gizpay-service-account-manager", File: "gizpay-service-account-manager.json", Audiences: []string{projects[1].ID}, Roles: []string{"credit_check", "charge"}},
		{ID: "gizway-cn-service", File: "gizway-cn-service.json", Audiences: []string{projects[1].ID}, Roles: []string{"credit_check", "charge"}},
		{ID: "gizway-global-service", File: "gizway-global-service.json", Audiences: []string{projects[1].ID}, Roles: []string{"credit_check", "charge"}},
		{ID: "gizway-cn-catalog", File: "gizway-cn-catalog.json", Audiences: []string{projects[0].ID}, Roles: []string{"public_catalog", "public_catalog_cn"}},
		{ID: "gizway-global-catalog", File: "gizway-global-catalog.json", Audiences: []string{projects[0].ID}, Roles: []string{"public_catalog", "public_catalog_global"}},
		{ID: "service-charger", File: "service-charger.json", Audiences: []string{projects[1].ID}, Roles: []string{"charge"}},
		{ID: "service-reader", File: "service-reader.json", Audiences: []string{projects[1].ID}, Roles: []string{"credit_check"}},
		{ID: "service-rotated", File: "service-rotated.json", Audiences: []string{projects[1].ID}, Roles: []string{"credit_check", "charge"}},
		{ID: "service-other-user", File: "service-other-user.json", Audiences: []string{projects[1].ID}, Roles: []string{"credit_check", "charge"}},
		{ID: "service-revoked", File: "service-revoked.json", Audiences: []string{projects[1].ID}, Roles: []string{"credit_check", "charge"}},
		{ID: "cn-administrator", Human: true, Audiences: []string{projects[2].ID, projects[3].ID}, Roles: []string{"administrator"}},
		{ID: "global-administrator", Human: true, Audiences: []string{projects[3].ID}, Roles: []string{"administrator"}},
		{ID: "wrong-project-administrator", Human: true, Audiences: []string{projects[2].ID}, GrantProjects: []string{projects[3].ID}, Roles: []string{"administrator"}},
		{ID: "revoked-administrator", Human: true, Audiences: []string{projects[2].ID}, Roles: []string{"administrator"}},
	}
	variables := map[string]string{}
	identityKeys := map[string]machineKey{}
	for _, identity := range identities {
		if identity.Human {
			userID, password, err := client.createHumanIdentity(identity)
			if err != nil {
				return err
			}
			variables[identity.ID+"@subject"] = userID
			variables[identity.ID+"@username"] = identity.ID
			variables[identity.ID+"@password"] = password
			for _, audience := range identity.Audiences {
				projectIndex := 0
				for index := range projects {
					if projects[index].ID == audience {
						projectIndex = index
						break
					}
				}
				accessToken, err := client.issueHumanToken(userID, password, applications[projectIndex], audience)
				if err != nil {
					return fmt.Errorf("issue Human %s token: %w", identity.ID, err)
				}
				variables[identity.ID+"@"+audience] = accessToken
			}
			continue
		}
		key, err := client.createIdentity(identity, options.outputDirectory)
		if err != nil {
			return err
		}
		identityKeys[identity.ID] = key
		variables[identity.ID+"@subject"] = key.UserID
		for _, audience := range identity.Audiences {
			accessToken, err := exchangeJWTBearer(context.Background(), options.zitadelURL, key, []string{"openid", "urn:zitadel:iam:org:projects:roles", projectAudienceScope(audience)})
			if err != nil {
				return fmt.Errorf("issue %s token: %w", identity.ID, err)
			}
			variables[identity.ID+"@"+audience] = accessToken
		}
	}
	for _, identityID := range []string{"gizway-cn-catalog", "gizway-global-catalog"} {
		clientID, clientSecret, err := client.generateMachineSecret(identityID)
		if err != nil {
			return fmt.Errorf("generate %s client secret: %w", identityID, err)
		}
		variables[identityID+"@client_id"] = clientID
		variables[identityID+"@client_secret"] = clientSecret
	}
	if err := os.WriteFile(filepath.Join(options.outputDirectory, "zitadel-action-signing-key"), []byte("pending-action-target"), 0600); err != nil {
		return err
	}
	if err := writeGizWayE2EConfigs(options.outputDirectory, variables, applications[0]); err != nil {
		return err
	}
	mapIdentityVariables(variables, projects, identityKeys)
	return writeVariables(filepath.Join(options.outputDirectory, "identity.vars"), variables)
}

func reusableZITADELFixtures(outputDirectory string) (bool, error) {
	variables, err := readVariables(filepath.Join(outputDirectory, "identity.vars"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read existing ZITADEL fixture output: %w", err)
	}
	for _, name := range []string{"human_subject", "human_token", "human_username", "human_password", "web_first_login_username", "web_first_login_password", "service_token", "cn_catalog_token", "global_catalog_token"} {
		if variables[name] == "" {
			return false, fmt.Errorf("existing ZITADEL fixture output is incomplete: %s is missing", name)
		}
	}
	for _, name := range []string{"gizpay-service-account-manager.json", "gizway-cn-service.json", "gizway-global-service.json", "gizway-cn-catalog.json", "gizway-global-catalog.json", "gizway-cn.yaml", "gizway-global.yaml"} {
		info, statErr := os.Stat(filepath.Join(outputDirectory, name))
		if statErr != nil || info.Size() == 0 {
			return false, fmt.Errorf("existing ZITADEL fixture output is incomplete: %s is missing or empty", name)
		}
	}
	return true, nil
}

func (c *zitadelClient) generateMachineSecret(userID string) (string, string, error) {
	var response struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if err := c.call(http.MethodPut, "/management/v1/users/"+userID+"/secret", map[string]any{}, &response, false); err != nil {
		return "", "", err
	}
	if response.ClientID == "" || response.ClientSecret == "" {
		return "", "", errors.New("ZITADEL returned an empty machine client secret")
	}
	return response.ClientID, response.ClientSecret, nil
}

func (c *zitadelClient) configureUserInitializationAction(outputDirectory string) error {
	const targetName = "GizPay Human initialization"
	type actionTarget struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		SigningKey string `json:"signingKey"`
	}
	var listed struct {
		Targets []actionTarget `json:"targets"`
	}
	if err := c.call(http.MethodPost, "/v2/actions/targets/search", map[string]any{}, &listed, false); err != nil {
		return fmt.Errorf("list ZITADEL Action V2 Targets: %w", err)
	}
	var target actionTarget
	for _, candidate := range listed.Targets {
		if candidate.Name == targetName {
			target = candidate
			break
		}
	}
	payload := map[string]any{
		"name":        targetName,
		"restWebhook": map[string]any{"interruptOnError": true},
		"endpoint":    "http://gizpay:8081/webhooks/v1/zitadel/user-authenticated",
		"timeout":     "10s", "payloadType": "PAYLOAD_TYPE_JSON",
	}
	if target.ID == "" {
		if err := c.call(http.MethodPost, "/v2/actions/targets", payload, &target, false); err != nil {
			return fmt.Errorf("create ZITADEL Action V2 Target: %w", err)
		}
	} else {
		var updated actionTarget
		if err := c.call(http.MethodPost, "/v2/actions/targets/"+target.ID, payload, &updated, false); err != nil {
			return fmt.Errorf("update ZITADEL Action V2 Target: %w", err)
		}
		if updated.SigningKey != "" {
			target.SigningKey = updated.SigningKey
		}
	}
	if target.ID == "" || target.SigningKey == "" {
		return errors.New("ZITADEL Action V2 Target returned no ID or signing key")
	}
	execution := map[string]any{"condition": map[string]any{"function": map[string]any{"name": "preaccesstoken"}}, "targets": []string{target.ID}}
	if err := c.call(http.MethodPut, "/v2/actions/executions", execution, nil, false); err != nil {
		return fmt.Errorf("set ZITADEL preaccesstoken execution: %w", err)
	}
	return os.WriteFile(filepath.Join(outputDirectory, "zitadel-action-signing-key"), []byte(target.SigningKey), 0600)
}

func writeGizWayE2EConfigs(outputDirectory string, values map[string]string, webClientID string) error {
	for _, region := range []string{"cn", "global"} {
		clientID, clientSecret := values["gizway-"+region+"-catalog@client_id"], values["gizway-"+region+"-catalog@client_secret"]
		if clientID == "" || clientSecret == "" {
			return fmt.Errorf("%s Public Catalog credentials are missing", region)
		}
		webPort := 3000
		if region == "cn" {
			webPort = 3001
		}
		webBaseURL := fmt.Sprintf("https://%s.e2e.gizclaw.test:%d", region, webPort)
		contents := fmt.Sprintf(`version: 1
server:
  name: %s.e2e.gizclaw.test
  listen_address: 0.0.0.0:8080
admin:
  initial_key_file: /fixtures/admin-key
site:
  hostname: %s.e2e.gizclaw.test
identity:
  issuer: https://identity.e2e.gizclaw.test:18080
  client_id: %s
  redirect_uri: %s/auth/callback
  post_logout_redirect_uri: %s/
  public_catalog_service_account:
    client_id: %s
    client_secret: %s
    access_token_type: JWT
    scope: "openid urn:zitadel:iam:org:projects:roles urn:zitadel:iam:org:project:id:386000000000000001:aud"
    token_ttl: 12h
    refresh_before: 1h
services:
  public_catalog_token_url: %s/auth/catalog-token
  gizpay_powersync_url: %s/_sync/gizpay
  gizpay_api_url: %s
  gizway_powersync_url: %s/_sync/gizway
  gizway_api_url: %s
database:
  dsn: postgres://gizway_%s_app:gizway_%s_app@postgres-%s:5432/gizway?sslmode=disable
  schema: gizway
authentication:
  zitadel:
    issuer: https://identity.e2e.gizclaw.test:18080
    jwks_url: http://oauth-spy:19500/oauth/v2/keys
    human_audience: "386000000000000001"
  service_account:
    token_url: http://oauth-spy:19500/oauth/v2/token
    private_key_file: /fixtures/gizway-%s-service.json
    audience: "386000000000000002"
    requested_scopes: [openid]
    required_roles: [credit_check, charge]
subscription_keys:
  hmac:
    secret_file: /secrets/subscription-key-hmac
gizpay:
  service_dsn: http://toxiproxy:8666
credit_cache:
  cleanup_interval: 1m
bifrost:
  config_store:
    type: postgresql
    dsn: postgres://gizway_%s_app:gizway_%s_app@postgres-%s:5432/gizway?sslmode=disable
    schema: bifrost_config
  log_store:
    type: postgresql
    dsn: postgres://gizway_%s_app:gizway_%s_app@postgres-%s:5432/gizway?sslmode=disable
    schema: bifrost_logs
`, region, region, webClientID, webBaseURL, webBaseURL, clientID, clientSecret, webBaseURL, webBaseURL, webBaseURL, webBaseURL, webBaseURL, region, region, region, region, region, region, region, region, region, region)
		if err := os.WriteFile(filepath.Join(outputDirectory, "gizway-"+region+".yaml"), []byte(contents), 0600); err != nil {
			return err
		}
	}
	return nil
}

type projectFixture struct {
	ID, Name string
	Roles    []string
}

type identityFixture struct {
	ID, File      string
	Audiences     []string
	GrantProjects []string
	Roles         []string
	Human         bool
}

func (c *zitadelClient) createOIDCApplication(projectID, name string) (string, error) {
	payload := map[string]any{
		"name": name, "redirectUris": []string{
			"http://127.0.0.1:18999/callback",
			"https://global.e2e.gizclaw.test:3000/auth/callback",
			"https://cn.e2e.gizclaw.test:3001/auth/callback",
		},
		"postLogoutRedirectUris": []string{
			"https://global.e2e.gizclaw.test:3000/",
			"https://cn.e2e.gizclaw.test:3001/",
		},
		"responseTypes": []string{"OIDC_RESPONSE_TYPE_CODE"}, "grantTypes": []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE"},
		"appType": "OIDC_APP_TYPE_NATIVE", "authMethodType": "OIDC_AUTH_METHOD_TYPE_NONE",
		"accessTokenType": "OIDC_TOKEN_TYPE_JWT", "accessTokenRoleAssertion": true, "devMode": true,
	}
	var response struct {
		ClientID string `json:"clientId"`
	}
	if err := c.call(http.MethodPost, "/management/v1/projects/"+projectID+"/apps/oidc", payload, &response, false); err != nil {
		return "", fmt.Errorf("create OIDC application for %s: %w", projectID, err)
	}
	if response.ClientID == "" {
		return "", errors.New("ZITADEL OIDC application response has no clientId")
	}
	return response.ClientID, nil
}

func (c *zitadelClient) createHumanIdentity(identity identityFixture) (string, string, error) {
	password := "Milestone03!" + strings.ReplaceAll(identity.ID, "-", "")
	payload := map[string]any{
		"userName": identity.ID, "profile": map[string]any{"firstName": identity.ID, "lastName": "E2E", "displayName": identity.ID},
		"email":    map[string]any{"email": identity.ID + "@example.test", "isEmailVerified": true},
		"password": password, "passwordChangeRequired": false,
	}
	var created struct {
		UserID string `json:"userId"`
	}
	if err := c.call(http.MethodPost, "/management/v1/users/human/_import", payload, &created, false); err != nil {
		return "", "", fmt.Errorf("create Human %s: %w", identity.ID, err)
	}
	grantProjects := identity.GrantProjects
	if len(grantProjects) == 0 {
		grantProjects = identity.Audiences
	}
	for _, projectID := range grantProjects {
		grant := map[string]any{"projectId": projectID, "roleKeys": identity.Roles}
		if err := c.call(http.MethodPost, "/management/v1/users/"+created.UserID+"/grants", grant, nil, false); err != nil {
			return "", "", fmt.Errorf("grant Human %s: %w", identity.ID, err)
		}
	}
	return created.UserID, password, nil
}

func (c *zitadelClient) issueHumanToken(userID, password, clientID, audience string) (string, error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return "", err
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	challenge := sha256.Sum256([]byte(verifier))
	state := "m03-" + base64.RawURLEncoding.EncodeToString(verifierBytes[:8])
	redirectURI := "http://127.0.0.1:18999/callback"
	query := url.Values{
		"client_id": {clientID}, "redirect_uri": {redirectURI}, "response_type": {"code"},
		"scope":          {"openid urn:zitadel:iam:org:projects:roles urn:zitadel:iam:org:project:role:administrator " + projectAudienceScope(audience)},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}, "state": {state},
	}
	redirectClient := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := redirectClient.Get(c.baseURL + "/oauth/v2/authorize?" + query.Encode())
	if err != nil {
		return "", err
	}
	response.Body.Close()
	location := response.Header.Get("Location")
	parsed, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	authRequestID := parsed.Query().Get("authRequest")
	if authRequestID == "" {
		authRequestID = parsed.Query().Get("authRequestID")
	}
	if authRequestID == "" {
		return "", fmt.Errorf("authorize did not return auth request: %s", location)
	}
	var session struct {
		SessionID    string `json:"sessionId"`
		SessionToken string `json:"sessionToken"`
	}
	checks := map[string]any{"checks": map[string]any{"user": map[string]any{"userId": userID}, "password": map[string]any{"password": password}}}
	var sessionErr error
	for range 30 {
		sessionErr = c.loginCall(http.MethodPost, "/v2/sessions", checks, &session)
		if sessionErr == nil {
			break
		}
		if !strings.Contains(sessionErr.Error(), "status 404") {
			return "", fmt.Errorf("create Human session: %w", sessionErr)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if sessionErr != nil {
		return "", fmt.Errorf("create Human session after waiting for ZITADEL projection: %w", sessionErr)
	}
	var callback struct {
		CallbackURL string `json:"callbackUrl"`
	}
	if err := c.loginCall(http.MethodPost, "/v2/oidc/auth_requests/"+url.PathEscape(authRequestID), map[string]any{"session": map[string]any{"sessionId": session.SessionID, "sessionToken": session.SessionToken}}, &callback); err != nil {
		return "", err
	}
	callbackURL, err := url.Parse(callback.CallbackURL)
	if err != nil {
		return "", err
	}
	if callbackURL.Query().Get("state") != state {
		return "", errors.New("OIDC state mismatch")
	}
	code := callbackURL.Query().Get("code")
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}
	tokenRequest, _ := http.NewRequest(http.MethodPost, c.baseURL+"/oauth/v2/token", strings.NewReader(form.Encode()))
	tokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResponse, err := c.http.Do(tokenRequest)
	if err != nil {
		return "", err
	}
	defer tokenResponse.Body.Close()
	var token struct {
		AccessToken string `json:"access_token"`
	}
	raw, _ := io.ReadAll(io.LimitReader(tokenResponse.Body, 1<<20))
	if tokenResponse.StatusCode != http.StatusOK || json.Unmarshal(raw, &token) != nil || token.AccessToken == "" {
		return "", fmt.Errorf("OIDC token exchange returned %d: %s", tokenResponse.StatusCode, raw)
	}
	return token.AccessToken, nil
}

func (c *zitadelClient) loginCall(method, path string, body, result any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(method, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.loginToken)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseRaw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("status %d: %s", response.StatusCode, strings.TrimSpace(string(responseRaw)))
	}
	if result != nil && len(responseRaw) != 0 {
		return json.Unmarshal(responseRaw, result)
	}
	return nil
}

func (c *zitadelClient) createProject(project projectFixture) error {
	body := map[string]any{"organizationId": c.orgID, "projectId": project.ID, "name": project.Name, "projectRoleAssertion": true, "authorizationRequired": false}
	if err := c.call(http.MethodPost, "/zitadel.project.v2.ProjectService/CreateProject", body, nil, true); err != nil && !strings.Contains(err.Error(), "already_exists") {
		return fmt.Errorf("create project %s: %w", project.ID, err)
	}
	for _, role := range project.Roles {
		payload := map[string]any{"roleKey": role, "displayName": role, "group": "milestone-03"}
		if err := c.call(http.MethodPost, "/management/v1/projects/"+project.ID+"/roles", payload, nil, false); err != nil && !strings.Contains(err.Error(), "already") {
			return fmt.Errorf("create role %s: %w", role, err)
		}
	}
	return nil
}

func (c *zitadelClient) lookupOrgID() error {
	var response struct {
		Org struct {
			ID string `json:"id"`
		} `json:"org"`
	}
	if err := c.call(http.MethodGet, "/management/v1/orgs/me", nil, &response, false); err != nil {
		return fmt.Errorf("get bootstrap organization: %w", err)
	}
	if response.Org.ID == "" {
		return errors.New("bootstrap organization response has no id")
	}
	c.orgID = response.Org.ID
	return nil
}

func (c *zitadelClient) createIdentity(identity identityFixture, outputDirectory string) (machineKey, error) {
	var created struct {
		UserID string `json:"userId"`
	}
	payload := map[string]any{"userId": identity.ID, "userName": identity.ID, "name": identity.ID, "accessTokenType": "ACCESS_TOKEN_TYPE_JWT"}
	if err := c.call(http.MethodPost, "/management/v1/users/machine", payload, &created, false); err != nil {
		return machineKey{}, fmt.Errorf("create identity %s: %w", identity.ID, err)
	}
	for _, projectID := range identity.Audiences {
		grant := map[string]any{"projectId": projectID, "roleKeys": identity.Roles}
		if err := c.call(http.MethodPost, "/management/v1/users/"+identity.ID+"/grants", grant, nil, false); err != nil {
			return machineKey{}, fmt.Errorf("grant identity %s: %w", identity.ID, err)
		}
	}
	var response struct {
		KeyID      string `json:"keyId"`
		KeyDetails string `json:"keyDetails"`
	}
	if err := c.call(http.MethodPost, "/management/v1/users/"+identity.ID+"/keys", map[string]any{"type": "KEY_TYPE_JSON"}, &response, false); err != nil {
		return machineKey{}, fmt.Errorf("create key %s: %w", identity.ID, err)
	}
	raw, err := base64.StdEncoding.DecodeString(response.KeyDetails)
	if err != nil {
		raw = []byte(response.KeyDetails)
	}
	var key machineKey
	if err := json.Unmarshal(raw, &key); err != nil {
		return machineKey{}, fmt.Errorf("decode key %s: %w", identity.ID, err)
	}
	if err := os.WriteFile(filepath.Join(outputDirectory, identity.File), raw, 0600); err != nil {
		return machineKey{}, err
	}
	return key, nil
}

func (c *zitadelClient) call(method, path string, body any, result any, connect bool) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var lastError error
	for range 80 {
		request, requestErr := http.NewRequest(method, c.baseURL+path, bytes.NewReader(raw))
		if requestErr != nil {
			return requestErr
		}
		request.Header.Set("Authorization", "Bearer "+c.token)
		request.Header.Set("Content-Type", "application/json")
		if connect {
			request.Header.Set("Connect-Protocol-Version", "1")
		}
		response, requestErr := c.http.Do(request)
		if requestErr != nil {
			lastError = requestErr
		} else {
			responseRaw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if readErr != nil {
				return readErr
			}
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				if result != nil && len(responseRaw) != 0 {
					return json.Unmarshal(responseRaw, result)
				}
				return nil
			}
			lastError = fmt.Errorf("status %d: %s", response.StatusCode, strings.TrimSpace(string(responseRaw)))
			if response.StatusCode < 500 {
				return lastError
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return lastError
}

func exchangeJWTBearer(ctx context.Context, baseURL string, key machineKey, scopes []string) (string, error) {
	privateKey, err := parsePrivateKey(key.Key)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	claims := jwt.MapClaims{"iss": key.UserID, "sub": key.UserID, "aud": strings.TrimRight(baseURL, "/"), "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()}
	assertion := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	assertion.Header["kid"] = key.KeyID
	signed, err := assertion.SignedString(privateKey)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {signed}, "scope": {strings.Join(scopes, " ")}}
	var lastError error
	// A newly-created Machine Key is committed before ZITADEL's authentication
	// projection can necessarily validate it. Keep retrying the transient
	// invalid_grant window long enough for a cold Compose startup.
	for range 120 {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/oauth/v2/token", strings.NewReader(form.Encode()))
		if err != nil {
			return "", err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			lastError = err
		} else {
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if readErr != nil {
				return "", readErr
			}
			var result struct {
				AccessToken string `json:"access_token"`
			}
			if json.Unmarshal(raw, &result) == nil && response.StatusCode == http.StatusOK && result.AccessToken != "" {
				return result.AccessToken, nil
			}
			lastError = fmt.Errorf("token endpoint returned %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return "", lastError
}

func readMachineKey(path string) (machineKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return machineKey{}, err
	}
	var key machineKey
	return key, json.Unmarshal(raw, &key)
}

func parsePrivateKey(value string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, errors.New("private key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return key, nil
}

func projectAudienceScope(projectID string) string {
	return "urn:zitadel:iam:org:project:id:" + projectID + ":aud"
}

func mapIdentityVariables(values map[string]string, projects []projectFixture, keys map[string]machineKey) {
	lookup := func(identity string, project int) string { return values[identity+"@"+projects[project].ID] }
	values["human_subject"] = values["human-primary@subject"]
	values["human_username"] = values["human-primary@username"]
	values["human_password"] = values["human-primary@password"]
	values["human_two_subject"] = values["human-two@subject"]
	values["web_first_login_subject"] = values["web-first-login@subject"]
	values["web_first_login_username"] = values["web-first-login@username"]
	values["web_first_login_password"] = values["web-first-login@password"]
	values["provider_merchant_human_subject"] = values["provider-merchant-human@subject"]
	values["provider_merchant_human_two_subject"] = values["provider-merchant-human-two@subject"]
	values["cn_admin_subject"] = values["cn-administrator@subject"]
	values["global_admin_subject"] = values["global-administrator@subject"]
	values["revoked_admin_subject"] = values["revoked-administrator@subject"]
	values["human_token"] = lookup("human-primary", 0)
	values["human_token_same_subject_changed_email"] = values["human_token"]
	values["human_token_two"] = lookup("human-two", 0)
	values["provider_merchant_human_token"] = lookup("provider-merchant-human", 0)
	values["provider_merchant_human_token_two"] = lookup("provider-merchant-human-two", 0)
	values["service_token"] = lookup("gizway-cn-service", 1)
	values["service_token_charger"] = lookup("service-charger", 1)
	values["service_token_reader_only"] = lookup("service-reader", 1)
	values["service_token_rotated"] = lookup("service-rotated", 1)
	values["service_token_other_user"] = lookup("service-other-user", 1)
	values["revoked_service_token"] = lookup("service-revoked", 1)
	values["service_subject"] = values["gizway-cn-service@subject"]
	values["global_service_subject"] = values["gizway-global-service@subject"]
	values["service_charger_subject"] = values["service-charger@subject"]
	values["service_reader_subject"] = values["service-reader@subject"]
	values["cn_admin_token"] = lookup("cn-administrator", 2)
	values["admin_token"] = values["cn_admin_token"]
	values["global_admin_token"] = lookup("global-administrator", 3)
	values["other_region_admin_token"] = lookup("global-administrator", 3)
	values["wrong_project_admin_token"] = lookup("wrong-project-administrator", 2)
	values["wrong_audience_admin_token"] = lookup("cn-administrator", 3)
	values["revoked_admin_token"] = lookup("revoked-administrator", 2)
	values["wrong_audience_service_token"] = values["global_admin_token"]
	values["cn_catalog_token"] = lookup("gizway-cn-catalog", 0)
	values["global_catalog_token"] = lookup("gizway-global-catalog", 0)
	values["wrong_issuer_service_token"] = signedFixtureToken(keys["gizway-cn-service"], "https://wrong-issuer.invalid", projects[1].ID, false)
	values["wrong_issuer_admin_token"] = signedInvalidToken("https://wrong-issuer.invalid", projects[2].ID, false)
	values["wrong_signature_service_token"] = signedInvalidToken("https://identity.e2e.gizclaw.test:18080", projects[1].ID, false)
	values["expired_service_token"] = signedFixtureToken(keys["gizway-cn-service"], "https://identity.e2e.gizclaw.test:18080", projects[1].ID, true)
}

func signedFixtureToken(key machineKey, issuer, audience string, expired bool) string {
	privateKey, err := parsePrivateKey(key.Key)
	if err != nil {
		return ""
	}
	now := time.Now().UTC()
	expires := now.Add(5 * time.Minute)
	if expired {
		expires = now.Add(-time.Minute)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuer, "sub": key.UserID, "aud": audience, "iat": now.Add(-2 * time.Minute).Unix(), "exp": expires.Unix()})
	token.Header["kid"] = key.KeyID
	signed, _ := token.SignedString(privateKey)
	return signed
}

func signedInvalidToken(issuer, audience string, expired bool) string {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	now := time.Now().UTC()
	expires := now.Add(5 * time.Minute)
	if expired {
		expires = now.Add(-time.Minute)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuer, "sub": "invalid-fixture", "aud": audience, "iat": now.Add(-2 * time.Minute).Unix(), "exp": expires.Unix()})
	token.Header["kid"] = "wrong-signature"
	signed, _ := token.SignedString(key)
	return signed
}

func writeVariables(path string, values map[string]string) error {
	var builder strings.Builder
	for key, value := range values {
		if strings.Contains(key, "@") {
			continue
		}
		fmt.Fprintf(&builder, "%s=%s\n", key, value)
	}
	return os.WriteFile(path, []byte(builder.String()), 0600)
}
