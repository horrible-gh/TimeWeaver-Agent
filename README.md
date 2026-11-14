# TimeWeaver Agent

A standalone Go agent for TimeWeaver. The server and web interface remain in the existing [TimeWeaver](https://github.com/horrible-gh/TimeWeaver) project. The agent runs without Python or a virtual environment and has no external Go module dependencies.

**This is the initial Go implementation.** It includes mock-server integration tests and Linux runtime tests. Production server migration and Windows process-control verification must be performed separately. The existing Python agent is not modified or stopped automatically.

## Build

The minimum Go language version is 1.23. Use a currently supported Go toolchain for release builds.

```sh
# Linux: runs git pull --ff-only before building by default
bash scripts/build.sh
# Build only the current checkout
bash scripts/build.sh --no-pull
```

```powershell
# Windows: stop the running agent in SpawnPoint before building
.\scripts\build.ps1
.\scripts\build.ps1 -NoPull
```

Output: `dist/timeweaver-agent` or `dist/timeweaver-agent.exe`.

## Set Up a New Device

```sh
./dist/timeweaver-agent init --server https://timeweaver.example.com/time_weaver --device batch-01
TIMEWEAVER_ENROLLMENT_TOKEN='your-one-time-enrollment-token' ./dist/timeweaver-agent run
```

Include the deployment's context path in the server URL. The agent enrolls automatically only when no stored credentials exist; subsequent runs use the saved credentials. Do not pass the token as a command-line argument or store it in a configuration file.

After enrollment, start the agent with:

```sh
./dist/timeweaver-agent run
```

On Windows, provide the initial token through SpawnPoint's environment-variable settings or PowerShell's `$env:TIMEWEAVER_ENROLLMENT_TOKEN`. Remove the one-time token from the startup environment after successful enrollment.

## Replace an Existing Python Agent

Stop the existing agent first and make sure there are no undelivered results. **The simplest migration is to keep the existing `TimeWeaver/agent` working directory and replace only the executable with the Go binary.**

| SpawnPoint Setting | Value |
|---|---|
| Executable | Absolute path to the new `TimeWeaver-Agent/dist/timeweaver-agent[.exe]` |
| Arguments | `run --config conf/time_weaver.json` |
| Working directory | The existing Python agent's working directory |
| OS account | The same account used by the existing agent |

The agent reads the existing `conf/time_weaver.json` and `conf/agent_credential.json` files without conversion. It does not re-enroll when a valid credential file is available. Python logging settings in `conf/server.json` are not used; logs are written to stderr.

**Do not run the Python and Go agents simultaneously with the same device credentials.** The Go file lock prevents duplicate Go agent instances only. You must stop the Python process separately.

## Diagnostics and Tests

```sh
./dist/timeweaver-agent check
./dist/timeweaver-agent version
go test -race ./...
go vet ./...
```

`check` only reads configuration and credential files. It does not make network requests, enroll a device, or delete files. All relative paths are resolved against the **process working directory**. Specifying an absolute path for `--config` does not change the base directory for other relative paths.

## Scope

The agent implements enrollment and token refresh, heartbeats, snapshot synchronization, scheduled and manual execution, FIFO result retries, and command/copy/ZIP/housekeep tasks. This implementation does not change the server API, database, web interface, FlowGate-Agent, Docker setup, or OS service registration.

The cleanup policy is unchanged. As in the Python agent, `housekeep` removes old files directly inside the specified directory. It does not recursively delete directories, protect files that are in use, or scan arbitrary locations under `/tmp`.

[Architecture](docs/architecture.md) · [Compatibility and Operational Notes](docs/compatibility.md) · [Verification Record](docs/verification.md)
