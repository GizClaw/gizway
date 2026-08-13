// Command gizway runs one Milestone 02 regional AI service.
package main

import (
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
	flags := flag.NewFlagSet("gizway", flag.ContinueOnError)
	configPath := flags.String("config", "", "YAML configuration file")
	serverName := flags.String("server-name", "", "override server.name")
	check := flags.Bool("check-config", false, "validate configuration and exit")
	initialize := flags.Bool("initialize", false, "initialize an empty database")
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
	config, err := app.LoadProcessConfig(*configPath, app.ProcessGizWay)
	if err != nil {
		return err
	}
	if *serverName != "" {
		config.Server.Name = *serverName
	}
	if initializeSet {
		config.Database.Initialize = *initialize
	}
	if err := app.ValidateProcessConfig(config, app.ProcessGizWay); err != nil {
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
	return app.RunProcess(config, app.ProcessGizWay)
}
