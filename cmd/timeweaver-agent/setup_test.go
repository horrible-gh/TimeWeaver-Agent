package main

import (
 "bytes"
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "sync/atomic"
 "testing"
 "time"

 "github.com/horrible-gh/TimeWeaver-Agent/internal/api"
 "github.com/horrible-gh/TimeWeaver-Agent/internal/config"
 "github.com/horrible-gh/TimeWeaver-Agent/internal/protocol"
 "github.com/horrible-gh/TimeWeaver-Agent/internal/securefile"
)

type fakePrompt struct { interactive bool; lines []string; token string; secrets int; err error }
func (p *fakePrompt) Interactive() bool { return p.interactive }
func (p *fakePrompt) Line(_ context.Context, _ string, fallback string) (string, error) {
 if p.err != nil { return "", p.err }
 if len(p.lines) == 0 { return fallback, nil }
 s := p.lines[0]; p.lines = p.lines[1:]; if s == "" { s = fallback }; return s, nil
}
func (p *fakePrompt) Secret(context.Context, string) (string, error) { p.secrets++; return p.token, p.err }

func setupDirectory(t *testing.T) {
 t.Helper()
 cwd, err := os.Getwd(); if err != nil { t.Fatal(err) }
 if err = os.Chdir(t.TempDir()); err != nil { t.Fatal(err) }
 t.Cleanup(func() { if e := os.Chdir(cwd); e != nil { t.Error(e) } })
 t.Setenv("TIMEWEAVER_ENROLLMENT_TOKEN", "")
}
func setupServer(t *testing.T, calls *atomic.Int32, reject *atomic.Bool) *httptest.Server {
 t.Helper()
 return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  calls.Add(1)
  if r.URL.Path != "/time_weaver/api/agent/v1/enroll" || r.Method != "POST" { t.Error(r.Method, r.URL.Path); http.NotFound(w, r); return }
  var body map[string]string
  if err := json.NewDecoder(r.Body).Decode(&body); err != nil { t.Error(err); w.WriteHeader(400); return }
  if body["enrollment_token"] == "" { t.Error("missing enrollment token") }
  if reject != nil && reject.Load() { w.WriteHeader(401); fmt.Fprint(w, `{"error":{"code":"enrollment_token_invalid"}}`); return }
  now := time.Now().UTC()
  response := map[string]any{"schema_version":"1", "server_time":now, "data":protocol.Credential{
   Identity:protocol.Identity{DeviceID:7, DeviceName:body["device_name"]},
   AccessToken:"access-secret", AccessTokenExpiresAt:now.Add(time.Hour),
   RefreshToken:"refresh-secret", RefreshTokenExpiresAt:now.Add(24*time.Hour),
  }}
  w.Header().Set("Content-Type", "application/json")
  _ = json.NewEncoder(w).Encode(response)
 }))
}
func TestSetupInteractiveAndRepeatPreservesRegistration(t *testing.T) {
 setupDirectory(t)
 var calls atomic.Int32
 server := setupServer(t, &calls, nil); defer server.Close()
 p := &fakePrompt{interactive:true, lines:[]string{server.URL,"batch-01"}, token:"enr-hidden"}
 var out bytes.Buffer
 opt := setupOptions{Path:"conf/time_weaver.json"}
 if err := setupAgent(context.Background(), opt, p, &out); err != nil { t.Fatal(err) }
 c, err := config.Load(opt.Path)
 if err != nil || c.Device != "batch-01" || c.API.BaseURL != server.URL+"/time_weaver" { t.Fatal(c, err) }
 cfg, _ := os.ReadFile(opt.Path); credential, err := securefile.ReadPrivate(c.API.CredentialPath)
 if err != nil { t.Fatal(err) }
 if strings.Contains(string(cfg), "secret") || strings.Contains(string(cfg), "enr-hidden") || strings.Contains(out.String(), "enr-hidden") { t.Fatal("secret leaked") }
 if p.secrets != 1 || calls.Load() != 1 { t.Fatal(p.secrets, calls.Load()) }
 if err = setupAgent(context.Background(), opt, &fakePrompt{}, &out); err != nil { t.Fatal(err) }
 afterCfg, _ := os.ReadFile(opt.Path); afterCred, _ := os.ReadFile(c.API.CredentialPath)
 if !bytes.Equal(cfg,afterCfg) || !bytes.Equal(credential,afterCred) || calls.Load() != 1 { t.Fatal("repeat setup changed registration") }
 opt.Server = "https://other.example/time_weaver"
 if err = setupAgent(context.Background(), opt, &fakePrompt{}, &out); err == nil { t.Fatal("server changed while keeping old identity") }
}
func TestSetupFailedEnrollmentCanResume(t *testing.T) {
 setupDirectory(t)
 var calls atomic.Int32; var reject atomic.Bool; reject.Store(true)
 server := setupServer(t, &calls, &reject); defer server.Close()
 var out bytes.Buffer
 opt := setupOptions{Path:"conf/time_weaver.json",Server:server.URL,Device:"batch-01",Token:"enr-bad"}
 err := setupAgent(context.Background(), opt, &fakePrompt{}, &out)
 if err == nil || !strings.Contains(err.Error(),"fresh token") || strings.Contains(err.Error(),opt.Token) { t.Fatal(err) }
 if _, err = config.Load(opt.Path); err != nil { t.Fatal("settings were not retained:",err) }
 if _, err = os.Stat("conf/agent_credential.json"); !os.IsNotExist(err) { t.Fatal(err) }
 reject.Store(false)
 opt.Server, opt.Device, opt.Token = "", "", "enr-good"
 if err = setupAgent(context.Background(), opt, &fakePrompt{}, &out); err != nil { t.Fatal(err) }
 if calls.Load() != 2 { t.Fatal(calls.Load()) }
}
func TestSetupMissingInputDoesNotWriteConfig(t *testing.T) {
 setupDirectory(t)
 var out bytes.Buffer
 opt := setupOptions{Path:"conf/time_weaver.json",Server:"https://example.test",Device:"host"}
 if err := setupAgent(context.Background(),opt,&fakePrompt{},&out); err == nil { t.Fatal("missing token accepted") }
 if _, err := os.Stat(opt.Path); !os.IsNotExist(err) { t.Fatal("wrote incomplete setup",err) }
}
func TestSetupPreservesUnknownConfiguration(t *testing.T) {
 setupDirectory(t)
 var calls atomic.Int32
 server := setupServer(t,&calls,nil); defer server.Close()
 if err := os.MkdirAll("conf",0700); err != nil { t.Fatal(err) }
 content := `{"device":"batch-01","timezone":"UTC","extension":{"keep":true},"api":{"base_url":"`+server.URL+`/time_weaver","future_setting":42,"heartbeat_interval":17}}`
 if err := os.WriteFile("conf/time_weaver.json",[]byte(content),0600); err != nil { t.Fatal(err) }
 var out bytes.Buffer
 if err := setupAgent(context.Background(),setupOptions{Path:"conf/time_weaver.json",Token:"token"},&fakePrompt{},&out); err != nil { t.Fatal(err) }
 b,_ := os.ReadFile("conf/time_weaver.json")
 for _, key := range []string{"extension","future_setting","heartbeat_interval","timezone"} { if !bytes.Contains(b,[]byte(key)) { t.Fatal("lost",key) } }
}
func TestSetupExpiredRegistrationNeedsConsent(t *testing.T) {
 setupDirectory(t)
 var calls atomic.Int32
 server := setupServer(t,&calls,nil); defer server.Close()
 c := config.Defaults(); c.Device = "batch-01"; c.API.BaseURL = server.URL+"/time_weaver"
 if err := writeSetupConfig("conf/time_weaver.json",c,false); err != nil { t.Fatal(err) }
 saved := protocol.StoredCredential{SchemaVersion:"1",Identity:protocol.Identity{DeviceID:7,DeviceName:c.Device},RefreshToken:"old-refresh",RefreshTokenExpiresAt:time.Now().Add(-time.Hour)}
 b,_ := json.Marshal(saved)
 if err := securefile.Write(c.API.CredentialPath,b,true); err != nil { t.Fatal(err) }
 var out bytes.Buffer
 opt := setupOptions{Path:"conf/time_weaver.json"}
 if err := setupAgent(context.Background(),opt,&fakePrompt{interactive:true,lines:[]string{"n"}},&out); err == nil { t.Fatal("replacement not confirmed") }
 after,_ := os.ReadFile(c.API.CredentialPath)
 if !bytes.Equal(b,after) || calls.Load()!=0 { t.Fatal("registration modified without consent") }
 if err := setupAgent(context.Background(),opt,&fakePrompt{interactive:true,lines:[]string{"y"},token:"fresh"},&out); err != nil { t.Fatal(err) }
 if calls.Load()!=1 { t.Fatal(calls.Load()) }
}
func TestSetupInvalidConfigAndCancelAreNonDestructive(t *testing.T) {
 setupDirectory(t)
 if err := os.WriteFile("broken.json",[]byte("not json"),0600); err != nil { t.Fatal(err) }
 var out bytes.Buffer
 if err := setupAgent(context.Background(),setupOptions{Path:"broken.json"},&fakePrompt{},&out); err==nil { t.Fatal("invalid config accepted") }
 b,_ := os.ReadFile("broken.json"); if string(b)!="not json" { t.Fatal("invalid config overwritten") }
 ctx,cancel := context.WithCancel(context.Background()); cancel()
 err := setupAgent(ctx,setupOptions{Path:"conf/time_weaver.json"},&fakePrompt{},&out)
 if !errors.Is(err,context.Canceled) { t.Fatal(err) }
}
func TestSetupRefusesConcurrentAgent(t *testing.T) {
 setupDirectory(t)
 lock,err := securefile.Acquire("conf/agent_credential.json.lock"); if err!=nil { t.Fatal(err) }; defer lock.Close()
 var out bytes.Buffer
 err=setupAgent(context.Background(),setupOptions{Path:"conf/time_weaver.json"},&fakePrompt{},&out)
 if err==nil || !strings.Contains(err.Error(),"already running") { t.Fatal(err) }
}
func TestEnrollmentTokenSourcesAndNoLeak(t *testing.T) {
 t.Setenv("TEST_TW_TOKEN","from-env")
 p:=&fakePrompt{interactive:true,token:"from-prompt"}
 token,err:=enrollmentToken(context.Background(),"explicit","TEST_TW_TOKEN",p)
 if err!=nil || token!="explicit" || p.secrets!=0 { t.Fatal(token,err) }
 token,err=enrollmentToken(context.Background(),"","TEST_TW_TOKEN",p)
 if err!=nil || token!="from-env" || p.secrets!=0 { t.Fatal(token,err) }
 t.Setenv("TEST_TW_TOKEN","")
 token,err=enrollmentToken(context.Background(),"","TEST_TW_TOKEN",p)
 if err!=nil || token!="from-prompt" || p.secrets!=1 { t.Fatal(token,err) }
 hint:=enrollmentHint(&api.Error{Code:"enr-secret-that-server-echoed"})
 if strings.Contains(hint.Error(),"enr-secret") { t.Fatal("server error leaked token") }
}
func TestNormalizeServer(t *testing.T) {
 for input,want:=range map[string]string{"https://example.test":"https://example.test/time_weaver","https://example.test/":"https://example.test/time_weaver","https://example.test/custom/":"https://example.test/custom"} {
  got,err:=normalizeServer(input); if err!=nil || got!=want { t.Fatal(input,got,err) }
 }
 for _,input:=range []string{"", "example.test", "ftp://example.test", "https://user:password@example.test", "https://example.test/?token=secret"} { if _,err:=normalizeServer(input);err==nil { t.Fatal(input) } }
}
func TestSetupCLIWithToken(t *testing.T) {
 setupDirectory(t)
 var calls atomic.Int32
 server:=setupServer(t,&calls,nil); defer server.Close()
 var out,errout bytes.Buffer
 if code:=run([]string{"setup","--server",server.URL,"--device","cli-device","--token","cli-secret","--config",filepath.Join("conf","time_weaver.json")},&out,&errout);code!=0 { t.Fatal(code,errout.String()) }
 if calls.Load()!=1 || strings.Contains(out.String()+errout.String(),"cli-secret") { t.Fatal("token leaked or registration missing") }
}
