# TimeWeaver Agent

A standalone Go agent for [TimeWeaver](https://github.com/horrible-gh/TimeWeaver). No Python, virtual environment, database credentials or external Go modules are required.

## First-time setup

In the TimeWeaver web interface, open **Devices > Agent registration**, choose the group and device name, and issue a registration token. Then run **one command** in the `TimeWeaver-Agent` checkout:

**Linux**

```sh
bash setup.sh
```

**Windows PowerShell**

```powershell
.\setup.ps1
```

The script builds the agent and opens the setup wizard:

```text
TimeWeaver Agent Setup
Server URL: https://timeweaver.example.com
Device name [your-hostname]: batch-01
Enrollment token (hidden):
Registering...
Registered. Credentials saved. Setup complete.
```

Use the same device name as the token issued by the web interface. The token is hidden while typing or pasting; press Enter to submit it. Setup saves connection settings and the resulting credential, not the enrollment token. A bare server origin receives the default `/time_weaver` context path; an explicit custom path is preserved.

Go 1.23 or newer must already be installed to build from source. Setup builds the current checkout; it does not pull, install an OS service, or start a second agent. Stop an existing agent before rebuilding on Windows.

Already have a binary? Run `timeweaver-agent setup` directly. Plain `init` also opens the wizard. An interactive first `run` (or launch without arguments) opens setup when configuration or credentials are missing, then starts the agent. Non-interactive service launches never wait for input.

## Start

```sh
bash run.sh
```

```powershell
.\run.ps1
```

These wrappers select the repository working directory automatically. Direct execution is also supported: `./dist/timeweaver-agent run` or `.\dist\timeweaver-agent.exe run`.

For SpawnPoint, setup prints the actual executable, arguments and working directory to enter. Use the **same OS account** that completed registration. Do not put the one-time token into the permanent startup command/environment.

| SpawnPoint setting | Value |
|---|---|
| Executable | Absolute path to `TimeWeaver-Agent/dist/timeweaver-agent[.exe]` |
| Arguments | `run --config conf/time_weaver.json` |
| Working directory | The `TimeWeaver-Agent` checkout used for setup |
| OS account | The account used for setup |

## Repeat setup / recover

Run the same setup command again. A valid stored registration is reused without spending a new token or changing existing settings. If enrollment failed, connection settings are kept so setup can resume. Expired registration requires confirmation before replacement. Unreadable or mismatched credentials are not deleted automatically.

For a registration that was revoked on the server, stop the agent, issue a fresh token, and use `timeweaver-agent setup --replace`. `enroll --replace` is also available and prompts for the token. Do not run two agents with the same credentials.

A server/device change must use a separate working directory, or an intentional recovery of the original configuration; setup never silently pairs old credentials with a new server/device.

## Automation and compatibility

Interactive hidden input is recommended. Scripts can provide the existing enrollment-token environment variable (default `TIMEWEAVER_ENROLLMENT_TOKEN`):

```sh
TIMEWEAVER_ENROLLMENT_TOKEN='one-time-token' ./dist/timeweaver-agent setup \
  --server https://timeweaver.example.com/time_weaver --device batch-01
```

`setup --token TOKEN` and `enroll --token TOKEN` are also supported. A command-line token may be visible in process listings and shell history. Neither method stores the enrollment token in the configuration. An existing custom `api.enrollment_token_env` remains supported.

Legacy `init --server URL [--device NAME]` remains configuration-only and refuses to overwrite a file. Use `setup` for the complete connection-and-registration flow.

## Build / update

```sh
git pull --ff-only
bash setup.sh
# Or build without running setup:
bash scripts/build.sh --no-pull
```

```powershell
git pull --ff-only
.\setup.ps1
# Or build without running setup:
.\scripts\build.ps1 -NoPull
```

The existing build scripts still pull with `--ff-only` when invoked without `--no-pull` / `-NoPull`. Output is `dist/timeweaver-agent[.exe]`.

## Replace an existing Python agent

Stop the Python agent and ensure results have been delivered. Keep its working directory and OS account; in SpawnPoint replace only the executable with the Go binary, with arguments `run --config conf/time_weaver.json`.

The existing `conf/time_weaver.json` and `conf/agent_credential.json` are read without conversion or re-enrollment. Python logging settings in `conf/server.json` are not used; logs go to stderr. The Go lock does not stop the Python process: stop it separately. The repository-root `run.sh` / `run.ps1` wrappers are for new installations, not migrations retaining the old Python working directory.

## Diagnostics and tests

```sh
./dist/timeweaver-agent check
./dist/timeweaver-agent version
go test -race ./...
go vet ./...
```

`check` is local and read-only: no network requests, enrollment or deletion. All relative paths use the **process working directory**, including when `--config` itself is absolute.

## Scope and verification boundary

The agent implements enrollment/token refresh, heartbeat and snapshot synchronization, scheduled/manual execution, FIFO result retries, and command/copy/ZIP/housekeep tasks. It does not alter the server API, database, FlowGate-Agent, Docker or OS service registration.

Mock-server tests do not establish a successful connection to your production server. Windows build/tests do not substitute for verification of your actual SpawnPoint account, console and process-control environment.

`housekeep` is non-recursive by default. In TimeWeaver **Tasks > Housekeep**, enable
**Include subfolders** to apply retention to regular files at every depth. Directories
(including empty ones), symbolic links, Windows junctions/reparse points and special
files are preserved. Files are judged individually by modification time; an old
folder never causes its newer files to be deleted. The selected root cannot be a
link. Permission errors fail the task, and cancellation is checked while walking.

Update/build and restart this agent **before** enabling the option on the updated
server. It advertises `housekeep_recursive_v1`; the server rejects recursive task
saves and snapshots for agents without support. Existing settings and credentials
are reused; no re-enrollment is needed. Both scheduled and manual runs use the flag.
An older server that omits `house_keep_recursive` keeps the previous behavior.

Housekeeping does not detect files in use or provide a filesystem security sandbox
against concurrent directory replacement. Use a dedicated cleanup directory owned
by the agent account, not arbitrary locations under `/tmp`.

[Architecture](docs/architecture.md) · [Compatibility](docs/compatibility.md) · [Verification](docs/verification.md)
