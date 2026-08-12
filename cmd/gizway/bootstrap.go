package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/idy/gizway/internal/storage"
	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

type bootstrapAdministratorConfig struct {
	PostgreSQLDSN, EmailFile, PasswordFile, DisplayName string
}

func bootstrapAdministratorConfigFromArgs(args []string, getenv func(string) string) (bootstrapAdministratorConfig, error) {
	flags := flag.NewFlagSet("gizway bootstrap-admin", flag.ContinueOnError)
	postgresDSN := flags.String("postgres-dsn", getenv("GIZWAY_POSTGRES_DSN"), "initialized regional GizWay PostgreSQL DSN")
	emailFile := flags.String("email-file", "", "file containing the initial regional administrator email")
	passwordFile := flags.String("password-file", "", "file containing the initial regional administrator password")
	displayName := flags.String("display-name", "Initial Regional Administrator", "initial regional administrator display name")
	if err := flags.Parse(args); err != nil {
		return bootstrapAdministratorConfig{}, err
	}
	if flags.NArg() != 0 || *postgresDSN == "" || *emailFile == "" || *passwordFile == "" || strings.TrimSpace(*displayName) == "" {
		return bootstrapAdministratorConfig{}, errors.New("postgres DSN, email file, password file, and display name are required")
	}
	return bootstrapAdministratorConfig{
		PostgreSQLDSN: *postgresDSN, EmailFile: *emailFile,
		PasswordFile: *passwordFile, DisplayName: strings.TrimSpace(*displayName),
	}, nil
}

// runBootstrapAdministrator is intentionally an offline database command.
// Deployment runs it once per regional database, so CN and Global retain
// independent operator authorization and no bootstrap secret crosses regions.
func runBootstrapAdministrator(args []string, getenv func(string) string, output io.Writer) error {
	config, err := bootstrapAdministratorConfigFromArgs(args, getenv)
	if err != nil {
		return err
	}
	emailBytes, err := os.ReadFile(config.EmailFile)
	if err != nil {
		return fmt.Errorf("read bootstrap administrator email: %w", err)
	}
	passwordBytes, err := os.ReadFile(config.PasswordFile)
	if err != nil {
		return fmt.Errorf("read bootstrap administrator password: %w", err)
	}
	defer clear(passwordBytes)
	email := strings.TrimSpace(string(emailBytes))
	password := strings.TrimSpace(string(passwordBytes))
	if email == "" || password == "" {
		return errors.New("bootstrap administrator email and password files must not be empty")
	}
	database, err := storage.OpenGizWayPostgreSQL(config.PostgreSQLDSN, false)
	if err != nil {
		return err
	}
	defer database.Close()
	administrator, replayed, err := store.New(database.SQL).BootstrapRegionalAdministrator(
		context.Background(), email, config.DisplayName, password, timetext.Format(time.Now()),
	)
	password = ""
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]any{
		"id": administrator.ID, "email": administrator.Email,
		"status": administrator.Status, "replayed": replayed,
	})
}
