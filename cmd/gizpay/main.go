// Command gizpay runs the Milestone 03 control plane.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/idy/gizway/internal/app"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("gizpay", flag.ContinueOnError)
	configPath := flags.String("config", "", "YAML configuration file")
	serverName := flags.String("server-name", "", "override server.name")
	check := flags.Bool("check-config", false, "validate configuration and exit")
	initialize := flags.Bool("initialize", false, "initialize an empty database")
	migrateOnly := flags.Bool("migrate-only", false, "apply service schema migrations and exit")
	printEffective := flags.String("print-effective-config", "", "print redacted effective configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	initializeSet := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == "initialize" {
			initializeSet = true
		}
	})
	if *migrateOnly && (initializeSet || *check || *printEffective != "") {
		return errors.New("--migrate-only cannot be combined with --initialize, --check-config, or --print-effective-config")
	}
	if *migrateOnly {
		config, err := app.LoadMigrationConfig(*configPath)
		if err != nil {
			return err
		}
		return app.RunMigrations(config, app.ProcessGizPay)
	}
	config, err := app.LoadProcessConfig(*configPath, app.ProcessGizPay)
	if err != nil {
		return err
	}
	if *serverName != "" {
		config.Server.Name = *serverName
	}
	if initializeSet {
		config.Database.Initialize = *initialize
	}
	if err := app.ValidateProcessConfig(config, app.ProcessGizPay); err != nil {
		return err
	}
	if *printEffective != "" {
		if *printEffective != "json" {
			return fmt.Errorf("unsupported effective config format %q", *printEffective)
		}
		return app.WriteEffectiveConfig(os.Stdout, config)
	}
	if *check {
		return nil
	}
	return app.RunProcess(config, app.ProcessGizPay)
}
