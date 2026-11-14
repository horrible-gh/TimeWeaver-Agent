package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/horrible-gh/TimeWeaver-Agent/internal/agent"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/api"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/config"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/credential"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/securefile"
)

var version = "0.1.0-dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, out, errout io.Writer) int {
	command := "run"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	if command == "help" || command == "--help" || command == "-h" {
		fmt.Fprintln(out, "TimeWeaver Agent\nUsage: timeweaver-agent <init|enroll|run|check|version> [options]\n  init --server URL [--device NAME] [--config conf/time_weaver.json]\n  enroll [--config PATH] [--replace]  (token from environment, never argv)\n  run [--config PATH]             (foreground; auto-enroll only if no credential)\n  check [--config PATH]             (local, read-only; no network)\nAll relative paths use the process working directory.")
		return 0
	}
	if command == "version" || command == "--version" {
		fmt.Fprintln(out, version)
		return 0
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(errout)
	path := flags.String("config", "conf/time_weaver.json", "configuration file")
	server, device := "", ""
	replace := false
	if command == "init" {
		flags.StringVar(&server, "server", "", "TimeWeaver server base URL, including context path")
		flags.StringVar(&device, "device", "", "device name (default hostname)")
	}
	if command == "enroll" {
		flags.BoolVar(&replace, "replace", false, "explicitly replace an existing credential after stopping the old agent")
	}
	if command != "init" && command != "enroll" && command != "run" && command != "check" {
		fmt.Fprintln(errout, "unknown command; use --help")
		return 2
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errout, "unexpected positional arguments")
		return 2
	}
	if command == "init" {
		if server == "" {
			fmt.Fprintln(errout, "--server is required")
			return 2
		}
		c := config.Defaults()
		c.API.BaseURL = server
		if device != "" {
			c.Device = device
		}
		if err := c.Validate(); err != nil {
			fmt.Fprintln(errout, err)
			return 2
		}
		if err := os.MkdirAll(filepath.Dir(*path), 0700); err != nil {
			fmt.Fprintln(errout, err)
			return 1
		}
		f, err := os.OpenFile(*path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			fmt.Fprintln(errout, err)
			return 1
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		err = enc.Encode(c)
		ce := f.Close()
		if err != nil || ce != nil {
			fmt.Fprintln(errout, "failed to write config")
			return 1
		}
		fmt.Fprintln(out, "Created", *path)
		return 0
	}
	c, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(errout, err)
		return 1
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(errout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if command == "check" {
		report := map[string]any{"configuration": "ok", "device": c.Device, "credential": "missing"}
		exit := 0
		b, e := securefile.ReadPrivate(c.API.CredentialPath)
		if e != nil {
			exit = 1
			if !os.IsNotExist(e) {
				report["credential"] = "unreadable"
			}
		} else {
			var stored protocol.StoredCredential
			if json.Unmarshal(b, &stored) != nil || stored.Validate() != nil || stored.DeviceName != c.Device {
				report["credential"] = "invalid"
				exit = 1
			} else {
				report["credential"] = "present"
				if !stored.RefreshTokenExpiresAt.After(time.Now()) {
					report["credential"] = "expired"
					exit = 1
				}
				report["device_id"] = stored.DeviceID
				report["refresh_token_expires_at"] = stored.RefreshTokenExpiresAt
			}
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return exit
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if command == "enroll" {
		lock, e := securefile.Acquire(c.API.CredentialPath + ".lock")
		if e != nil {
			fmt.Fprintln(errout, e)
			return 1
		}
		defer lock.Close()
		mgr := credential.New(c, api.New(c, version))
		if e = mgr.Enroll(ctx, os.Getenv(c.API.EnrollmentTokenEnv), replace); e != nil {
			fmt.Fprintln(errout, "Enrollment failed:", e)
			return 1
		}
		fmt.Fprintln(out, "Enrollment succeeded.")
		return 0
	}
	if err = agent.New(c, version).Run(ctx); err != nil {
		fmt.Fprintln(errout, err)
		return 1
	}
	return 0
}
