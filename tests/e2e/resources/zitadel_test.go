package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReusableZITADELFixtures(t *testing.T) {
	directory := t.TempDir()
	reusable, err := reusableZITADELFixtures(directory)
	if err != nil || reusable {
		t.Fatalf("empty directory: reusable=%v err=%v", reusable, err)
	}
	variables := "human_subject=human\nhuman_token=token\nhuman_username=user\nhuman_password=password\nbrowser_client_id=browser\nbrowser_client_first_login_username=new-user\nbrowser_client_first_login_password=new-password\nservice_token=service\ncn_catalog_token=cn\nglobal_catalog_token=global\n"
	if err := os.WriteFile(filepath.Join(directory, "identity.vars"), []byte(variables), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gizpay-service-account-manager.json", "gizway-cn-service.json", "gizway-global-service.json", "gizway-cn-catalog.json", "gizway-global-catalog.json", "gizway-cn.yaml", "gizway-global.yaml"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reusable, err = reusableZITADELFixtures(directory)
	if err != nil || !reusable {
		t.Fatalf("complete directory: reusable=%v err=%v", reusable, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "gizway-global.yaml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if reusable, err = reusableZITADELFixtures(directory); err == nil || reusable {
		t.Fatalf("empty required output: reusable=%v err=%v", reusable, err)
	}
}

func TestWriteGizWayE2EConfigsUsesAdvertisedEntryPorts(t *testing.T) {
	directory := t.TempDir()
	values := map[string]string{
		"gizway-cn-catalog@client_id":         "cn-client",
		"gizway-cn-catalog@client_secret":     "cn-secret",
		"gizway-global-catalog@client_id":     "global-client",
		"gizway-global-catalog@client_secret": "global-secret",
	}
	if err := writeGizWayE2EConfigs(directory, values, 33000, 33001); err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]string{"gizway-global.yaml": "https://global.e2e.gizclaw.test:33000", "gizway-cn.yaml": "https://cn.e2e.gizclaw.test:33001"} {
		contents, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("%s does not advertise %s", name, expected)
		}
	}
	if err := writeGizWayE2EConfigs(directory, values, 0, 33001); err == nil {
		t.Fatal("invalid Entry port was accepted")
	}
}
