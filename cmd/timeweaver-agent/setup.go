package main

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/url"
 "os"
 "path/filepath"
 "runtime"
 "strings"
 "time"

 "github.com/horrible-gh/TimeWeaver-Agent/internal/api"
 "github.com/horrible-gh/TimeWeaver-Agent/internal/config"
 "github.com/horrible-gh/TimeWeaver-Agent/internal/credential"
 "github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
 "github.com/horrible-gh/TimeWeaver-Agent/internal/securefile"
)

type setupPrompt interface {
 Interactive() bool
 Line(context.Context, string, string) (string, error)
 Secret(context.Context, string) (string, error)
}
type setupOptions struct { Path, Server, Device, Token string; Replace bool }

func setupAgent(ctx context.Context, opt setupOptions, p setupPrompt, out io.Writer) error {
 if err := ctx.Err(); err != nil { return err }
 c, err := config.Load(opt.Path)
 exists := err == nil
 if err != nil && !errors.Is(err, os.ErrNotExist) { return fmt.Errorf("existing configuration was not changed: %w", err) }
 if !exists { c = config.Defaults() }
 // Keep the same lock as run/enroll, including across prompts and config writes.
 lock, err := securefile.Acquire(c.API.CredentialPath + ".lock")
 if err != nil { return err }; defer lock.Close()
 fmt.Fprintln(out, "TimeWeaver Agent Setup")
 raw, err := securefile.ReadPrivate(c.API.CredentialPath)
 hasCredential := err == nil
 if err != nil && !os.IsNotExist(err) { return fmt.Errorf("cannot read stored credentials; check the OS account and file permissions: %w", err) }
 if hasCredential {
  if !exists { return errors.New("credentials already exist but configuration is missing; restore the original configuration before setup (credentials were not changed)") }
  var saved protocol.StoredCredential
  if json.Unmarshal(raw, &saved) != nil || saved.Validate() != nil || saved.DeviceName != c.Device {
   return errors.New("stored credentials do not match this configuration; restore the original configuration or use enroll --replace explicitly (nothing was changed)")
  }
  // Do not pair an old credential with a new server/device, even with --replace.
  if opt.Server != "" { normalized, e := normalizeServer(opt.Server); if e != nil { return e }; if normalized != strings.TrimRight(c.API.BaseURL, "/") { return errors.New("this device is already registered with a different server; use a separate working directory for another registration") } }
  if opt.Device != "" && strings.TrimSpace(opt.Device) != c.Device { return errors.New("this device is already registered under another name; its configuration was not changed") }
  if saved.RefreshTokenExpiresAt.After(time.Now()) && !opt.Replace {
   fmt.Fprintln(out, "Stored registration found; no new token needed.")
   printStart(out, opt.Path)
   return nil
  }
  if !opt.Replace {
   if !p.Interactive() { return errors.New("registration expired; run setup in a terminal or provide --replace with a fresh token") }
   answer, e := p.Line(ctx, "Registration expired. Replace it? (y/N)", "N")
   if e != nil { return e }
   if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") { return errors.New("cancelled; existing registration was kept") }
   opt.Replace = true
  }
 } else {
  if opt.Server != "" { c.API.BaseURL = opt.Server } else if p.Interactive() {
   fallback := ""; if exists { fallback = c.API.BaseURL }
   c.API.BaseURL, err = p.Line(ctx, "Server URL", fallback)
   if err != nil { return err }
  } else if !exists { return errors.New("server URL is required; run setup in a terminal or pass --server URL") }
  c.API.BaseURL, err = normalizeServer(c.API.BaseURL)
  if err != nil { return err }
  if opt.Device != "" { c.Device = strings.TrimSpace(opt.Device) } else if p.Interactive() {
   c.Device, err = p.Line(ctx, "Device name", c.Device)
   if err != nil { return err }
  }
 }
 if err = c.Validate(); err != nil { return err }
 token, err := enrollmentToken(ctx, opt.Token, c.API.EnrollmentTokenEnv, p)
 if err != nil { return err }
 if err = ctx.Err(); err != nil { return err }
 // Save only non-secret connection settings before using the one-time token.
 // A network failure can then resume setup without retyping everything.
 if err = writeSetupConfig(opt.Path, c, exists); err != nil { return err }
 if err = credentialWritable(c.API.CredentialPath); err != nil { return fmt.Errorf("cannot save credentials; registration was not attempted: %w", err) }
 fmt.Fprintln(out, "Registering...")
 mgr := credential.New(c, api.New(c, version))
 if err = mgr.Enroll(ctx, token, opt.Replace); err != nil { return enrollmentHint(err) }
 _ = os.Unsetenv(c.API.EnrollmentTokenEnv)
 fmt.Fprintln(out, "Registered. Credentials saved. Setup complete.")
 printStart(out, opt.Path)
 return nil
}

func enrollmentToken(ctx context.Context, explicit, env string, p setupPrompt) (string, error) {
 token := strings.TrimSpace(explicit)
 if token == "" { token = strings.TrimSpace(os.Getenv(env)) }
 if token == "" && p.Interactive() {
  var err error
  token, err = p.Secret(ctx, "Enrollment token")
  if err != nil { return "", err }
  token = strings.TrimSpace(token)
 }
 if token == "" { return "", fmt.Errorf("an enrollment token is required; run setup in a terminal or supply --token / %s", env) }
 return token, nil
}

func normalizeServer(value string) (string, error) {
 u, err := url.Parse(strings.TrimSpace(value))
 if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
  return "", errors.New("enter an http:// or https:// server URL without credentials, query or fragment")
 }
 if u.Path == "" || u.Path == "/" { u.Path = "/time_weaver" }
 u.Path = strings.TrimRight(u.Path, "/")
 // A configured custom context path is otherwise preserved verbatim.
 return strings.TrimRight(u.String(), "/"), nil
}

func writeSetupConfig(path string, c config.Config, exists bool) error {
 var content []byte
 var err error
 if exists {
  // Preserve unknown fields and tuning options from the existing/Python config.
  old, e := os.ReadFile(path); if e != nil { return e }
  var fields map[string]json.RawMessage
  if e = json.Unmarshal(old, &fields); e != nil { return e }
  var apiFields map[string]json.RawMessage
  if b, ok := fields["api"]; ok { if e = json.Unmarshal(b, &apiFields); e != nil { return e } }
  if apiFields == nil { apiFields = map[string]json.RawMessage{} }
  fields["device"], _ = json.Marshal(c.Device)
  apiFields["base_url"], _ = json.Marshal(c.API.BaseURL)
  fields["api"], _ = json.Marshal(apiFields)
  content, err = json.MarshalIndent(fields, "", "  ")
 } else { content, err = json.MarshalIndent(c, "", "  ") }
 if err != nil { return err }
 return securefile.Write(path, append(content, '\n'), true)
}

func credentialWritable(path string) error {
 dir := filepath.Dir(path)
 if err := os.MkdirAll(dir, 0700); err != nil { return err }
 f, err := os.CreateTemp(dir, ".setup-write-check-*")
 if err != nil { return err }
 name := f.Name()
 err = f.Close()
 if e := os.Remove(name); err == nil { err = e }
 return err
}

func enrollmentHint(err error) error {
 if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) { return err }
 // Never print the server's free-form response or an untrusted error code: it
 // could include the submitted token. Provide actionable, fixed messages instead.
 switch api.Code(err) {
 case "enrollment_token_invalid", "invalid_token", "token_expired":
  return errors.New("token was rejected, used or expired. Issue a fresh token in Devices > Agent registration, then run setup again")
 case "unavailable", "not_found", "malformed_response", "schema_mismatch":
  return errors.New("could not connect to the agent API. Check the server URL/context path and trusted TLS certificate, then run setup again; settings were kept")
 case "device_name_mismatch", "enrollment_device_name_mismatch":
  return errors.New("device name does not match the issued token. Use the name shown in Devices > Agent registration")
 case "credential_persist_failed":
  return errors.New("registration was accepted but credentials could not be saved. Check directory permissions before issuing a fresh token")
 default:
  return errors.New("registration failed. Check the server/device and use a fresh registration token; settings were kept. An existing registration requires enroll --replace")
 }
}

func printStart(out io.Writer, path string) {
 exe, err := os.Executable(); if err != nil { exe = "timeweaver-agent" }
 cwd, _ := os.Getwd()
 prefix := ""; if runtime.GOOS == "windows" { prefix = "& " }
 fmt.Fprintf(out, "\nStart: %s%q run --config %q\n", prefix, exe, path)
 fmt.Fprintf(out, "SpawnPoint executable: %s\nArguments: run --config %q\nWorking directory: %s\n", exe, path, cwd)
}
