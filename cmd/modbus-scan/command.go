package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"modbus-scan/internal/appconfig"
	"modbus-scan/internal/winservice"
)

const serviceCommandTimeout = 30 * time.Second

func dispatchCommand(ctx context.Context, args []string, executablePath string, manager winservice.Manager, stdout io.Writer) (bool, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return false, nil
	}

	command := args[0]
	commandArgs := args[1:]
	switch command {
	case "install":
		flags := flag.NewFlagSet("install", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		configPath := flags.String("config", "configs/config.toml", "service TOML configuration path")
		if err := flags.Parse(commandArgs); err != nil {
			writeServiceUsage(stdout)
			return true, fmt.Errorf("parse install arguments: %w", err)
		}
		if flags.NArg() != 0 {
			writeServiceUsage(stdout)
			return true, fmt.Errorf("install does not accept positional arguments")
		}
		absExecutable, err := filepath.Abs(executablePath)
		if err != nil {
			return true, fmt.Errorf("resolve executable path: %w", err)
		}
		absConfig, err := filepath.Abs(*configPath)
		if err != nil {
			return true, fmt.Errorf("resolve config path: %w", err)
		}
		if _, err := appconfig.Load(absConfig); err != nil {
			return true, fmt.Errorf("validate service config: %w", err)
		}
		if err := manager.Install(absExecutable, absConfig); err != nil {
			return true, fmt.Errorf("install service: %w", err)
		}
		return true, nil
	case "uninstall":
		if err := requireNoArguments(command, commandArgs); err != nil {
			writeServiceUsage(stdout)
			return true, err
		}
		if err := manager.Uninstall(); err != nil {
			return true, fmt.Errorf("uninstall service: %w", err)
		}
		return true, nil
	case "start":
		if err := requireNoArguments(command, commandArgs); err != nil {
			writeServiceUsage(stdout)
			return true, err
		}
		if err := manager.Start(); err != nil {
			return true, fmt.Errorf("start service: %w", err)
		}
		return true, nil
	case "stop":
		if err := requireNoArguments(command, commandArgs); err != nil {
			writeServiceUsage(stdout)
			return true, err
		}
		commandCtx, cancel := context.WithTimeout(ctx, serviceCommandTimeout)
		defer cancel()
		if err := manager.Stop(commandCtx); err != nil {
			return true, fmt.Errorf("stop service: %w", err)
		}
		return true, nil
	case "restart":
		if err := requireNoArguments(command, commandArgs); err != nil {
			writeServiceUsage(stdout)
			return true, err
		}
		commandCtx, cancel := context.WithTimeout(ctx, serviceCommandTimeout)
		defer cancel()
		if err := manager.Restart(commandCtx); err != nil {
			return true, fmt.Errorf("restart service: %w", err)
		}
		return true, nil
	case "status":
		if err := requireNoArguments(command, commandArgs); err != nil {
			writeServiceUsage(stdout)
			return true, err
		}
		state, err := manager.Status()
		if err != nil {
			return true, fmt.Errorf("query service status: %w", err)
		}
		if _, err := fmt.Fprintln(stdout, state); err != nil {
			return true, fmt.Errorf("write service status: %w", err)
		}
		return true, nil
	default:
		writeServiceUsage(stdout)
		return true, fmt.Errorf("unknown command %q", command)
	}
}

func writeServiceUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  modbus-scan install [-config <path>]")
	fmt.Fprintln(w, "  modbus-scan uninstall|start|stop|restart|status")
}

func requireNoArguments(command string, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("%s does not accept arguments", command)
	}
	return nil
}
