// Command gizway runs one Milestone 03 regional AI service.
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
	if len(args) == 0 {
		return errors.New("command is required: serve or init")
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "init":
		return runInit(args[1:])
	default:
		return fmt.Errorf("unsupported command %q: want serve or init", args[0])
	}
}

func runInit(args []string) error {
	flags := flag.NewFlagSet("gizway init", flag.ContinueOnError)
	configPath := flags.String("config", "", "YAML configuration file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("gizway init accepts no positional arguments")
	}
	config, err := app.LoadInitConfig(*configPath, app.ProcessGizWay)
	if err != nil {
		return err
	}
	return app.RunInitialization(config, app.ProcessGizWay)
}

func runServe(args []string) error {
	flags := flag.NewFlagSet("gizway", flag.ContinueOnError)
	configPath := flags.String("config", "", "YAML configuration file")
	serverName := flags.String("server-name", "", "override server.name")
	check := flags.Bool("check-config", false, "validate configuration and exit")
	printEffective := flags.String("print-effective-config", "", "print redacted effective configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, err := app.LoadProcessConfig(*configPath, app.ProcessGizWay)
	if err != nil {
		return err
	}
	if *serverName != "" {
		config.Server.Name = *serverName
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
