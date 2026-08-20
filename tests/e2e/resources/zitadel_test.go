package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReusableZITADELFixtures(t *testing.T) {
	directory := t.TempDir()
	reusable, err := reusableZITADELFixtures(directory)
	if err != nil || reusable {
		t.Fatalf("empty directory: reusable=%v err=%v", reusable, err)
	}
	variables := "human_subject=human\nhuman_token=token\nhuman_username=user\nhuman_password=password\nweb_first_login_username=new-user\nweb_first_login_password=new-password\nservice_token=service\ncn_catalog_token=cn\nglobal_catalog_token=global\n"
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
