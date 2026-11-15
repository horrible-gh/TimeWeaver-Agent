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
	"github.com/horrible-gh/TimeWeaver-Agent/internal/console"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/credential"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
	"github.com/horrible-gh/TimeWeaver-Agent/internal/securefile"
)

var version = "0.1.0-dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, out, errout io.Writer) int {
	command := "run"
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}
	if command == "help" || command == "--help" || command == "-h" {
		fmt.Fprintln(out, `TimeWeaver Agent
Usage: timeweaver-agent <setup|run|check|init|enroll|version> [options]
  setup                          Ask for server, device and token; register once
  run [--config PATH]             Start the agent (first-run setup in a terminal)
  check [--config PATH]           Local, read-only configuration/credential check
  init                           Same interactive setup as 'setup'
  init --server URL [--device NAME]  Legacy config-only mode; never overwrites
  enroll [--config PATH] [--replace] Re-register; asks for a token if needed
Setup options: --server URL --device NAME --config PATH --token TOKEN --replace
Prefer hidden token input. --token can be visible in process lists/shell history.
Unattended setup also accepts TIMEWEAVER_ENROLLMENT_TOKEN.
All relative paths use the process working directory.`)
		return 0
	}
	if command == "version" || command == "--version" {
		fmt.Fprintln(out, version)
		return 0
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(errout)
	path := flags.String("config", "conf/time_weaver.json", "configuration file")
	server, device, token := "", "", ""
	replace := false
	if command == "setup" || command == "init" {
		flags.StringVar(&server, "server", "", "TimeWeaver server URL (default context: /time_weaver)")
		flags.StringVar(&device, "device", "", "device name (default hostname)")
	}
	if command == "setup" || command == "init" || command == "enroll" {
		flags.StringVar(&token, "token", "", "one-time enrollment token (hidden interactive input preferred)")
	}
	if command == "setup" || command == "enroll" {
		flags.BoolVar(&replace, "replace", false, "replace registration after stopping the old agent")
	}
	if command != "setup" && command != "init" && command != "enroll" && command != "run" && command != "check" {
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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	prompt := console.New(os.Stdin, out)
	// Keep existing automation compatible; plain init now offers the complete wizard.
	if command == "setup" || (command == "init" && (server == "" || token != "")) {
		if err := setupAgent(ctx, setupOptions{Path: *path, Server: server, Device: device, Token: token, Replace: replace}, prompt, out); err != nil {
			fmt.Fprintln(errout, "Setup:", err)
			return 1
		}
		return 0
	}
	if command == "init" {
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
		fmt.Fprintln(out, "Next: timeweaver-agent setup --config", *path)
		return 0
	}
	c, err := config.Load(*path)
	if command == "run" && errors.Is(err, os.ErrNotExist) {
		if !prompt.Interactive() {
			fmt.Fprintln(errout, "Not configured. Run timeweaver-agent setup in a terminal first.")
			return 1
		}
		if err = setupAgent(ctx, setupOptions{Path: *path}, prompt, out); err == nil {
			c, err = config.Load(*path)
		}
	}
	if err != nil {
		fmt.Fprintln(errout, err)
		return 1
	}
	// A saved config without credentials is a resumable first run, not an opaque retry loop.
	if command == "run" && prompt.Interactive() && os.Getenv(c.API.EnrollmentTokenEnv) == "" {
		if _, e := os.Lstat(c.API.CredentialPath); os.IsNotExist(e) {
			if err = setupAgent(ctx, setupOptions{Path: *path}, prompt, out); err != nil {
				fmt.Fprintln(errout, "Setup:", err)
				return 1
			}
			c, err = config.Load(*path)
			if err != nil {
				fmt.Fprintln(errout, err)
				return 1
			}
		}
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
	if command == "enroll" {
		lock, e := securefile.Acquire(c.API.CredentialPath + ".lock")
		if e != nil {
			fmt.Fprintln(errout, e)
			return 1
		}
		defer lock.Close()
		token, e = enrollmentToken(ctx, token, c.API.EnrollmentTokenEnv, prompt)
		if e != nil {
			fmt.Fprintln(errout, e)
			return 1
		}
		mgr := credential.New(c, api.New(c, version))
		if e = mgr.Enroll(ctx, token, replace); e != nil {
			fmt.Fprintln(errout, enrollmentHint(e))
			return 1
		}
		_ = os.Unsetenv(c.API.EnrollmentTokenEnv)
		fmt.Fprintln(out, "Enrollment succeeded.")
		return 0
	}
	if err = agent.New(c, version).Run(ctx); err != nil {
		fmt.Fprintln(errout, err)
		if api.Code(err) == "needs_enrollment" {
			fmt.Fprintln(errout, "Run timeweaver-agent setup to register this device.")
		}
		return 1
	}
	return 0
}
