# TimeWeaver Agent

TimeWeaver의 독립형 Go Agent. 서버·웹은 기존 [TimeWeaver](https://github.com/horrible-gh/TimeWeaver)를 사용합니다. Python이나 가상환경 없이 실행하며, Go 외부 모듈 의존성은 없습니다.

**초기 Go 구현입니다.** 모의 서버 통합 테스트와 Linux 실행 테스트를 포함합니다. 실제 운영 서버 교체 및 Windows 프로세스 제어 검증은 별도로 진행해야 합니다. 기존 Python Agent는 자동으로 변경·중지하지 않습니다.

## 빌드

Go 언어 기준은 1.23 이상입니다. 배포 빌드에는 현재 지원되는 Go toolchain을 사용하세요.

```sh
# Linux: 기본적으로 git pull --ff-only 후 빌드
bash scripts/build.sh
# 현재 checkout만 빌드
bash scripts/build.sh --no-pull
```

```powershell
# Windows: 실행 중인 Agent는 SpawnPoint에서 먼저 중지
.\scripts\build.ps1
.\scripts\build.ps1 -NoPull
```

결과: `dist/timeweaver-agent` 또는 `dist/timeweaver-agent.exe`.

## 새 장치

```sh
./dist/timeweaver-agent init --server https://timeweaver.example.com/time_weaver --device batch-01
TIMEWEAVER_ENROLLMENT_TOKEN='발급받은 일회용 토큰' ./dist/timeweaver-agent run
```

서버 주소에는 실제 배포의 context path를 포함합니다. 등록 정보가 없을 때만 자동 등록하고 이후에는 저장된 인증 정보를 사용합니다. 토큰을 명령행 인자나 설정 파일에 저장하지 않습니다.

등록 후에는 다음 명령만 실행합니다.

```sh
./dist/timeweaver-agent run
```

Windows에서는 SpawnPoint의 환경변수 설정 또는 PowerShell의 `$env:TIMEWEAVER_ENROLLMENT_TOKEN`으로 최초 토큰을 전달합니다. 등록 성공 후 시작 환경에서 일회용 토큰을 제거하세요.

## 기존 Python Agent 교체

기존 Agent를 먼저 중지하고 미전송 결과가 없는지 확인합니다. **가장 단순한 전환은 작업 디렉터리를 기존 `TimeWeaver/agent`로 유지하고 실행 파일만 Go 바이너리로 교체하는 것입니다.**

| SpawnPoint 항목 | 값 |
|---|---|
| 실행 파일 | 새 `TimeWeaver-Agent/dist/timeweaver-agent[.exe]`의 절대 경로 |
| 인자 | `run --config conf/time_weaver.json` |
| 작업 디렉터리 | 기존 Python Agent의 작업 디렉터리 |
| OS 계정 | 기존 Agent와 동일 계정 |

기존 `conf/time_weaver.json`과 `conf/agent_credential.json`을 그대로 읽습니다. 유효한 인증 파일이 있으면 재등록하지 않습니다. `conf/server.json`의 Python 로깅 설정은 사용하지 않으며, 로그는 stderr로 출력합니다.

**Python과 Go Agent를 같은 장치 인증 정보로 동시에 실행하지 마세요.** Go의 파일 잠금은 Go Agent끼리의 중복 실행만 막습니다. Python 프로세스는 직접 중지해야 합니다.

## 진단 및 테스트

```sh
./dist/timeweaver-agent check
./dist/timeweaver-agent version
go test -race ./...
go vet ./...
```

`check`는 설정·인증 파일만 읽으며 네트워크 접속, 등록, 삭제를 하지 않습니다. 상대 경로는 모두 **프로세스 작업 디렉터리** 기준입니다. `--config`만 절대 경로로 지정해도 나머지 상대 경로의 기준은 바뀌지 않습니다.

## 범위

등록·토큰 갱신, heartbeat, 스냅샷 동기화, 정기·수동 실행, 결과 FIFO 재전송, 명령/복사/ZIP/housekeep를 구현합니다. 서버 API·DB·웹, FlowGate-Agent, Docker, OS 서비스 등록은 변경하지 않습니다.

정리 정책을 확대하지 않습니다. `housekeep`는 기존처럼 지정 경로 바로 아래의 오래된 파일을 정리하며, 디렉터리 재귀 삭제·실행 중 파일 보호·임의의 `/tmp` 탐색 기능이 아닙니다.

[설계](docs/architecture.md) · [호환성과 운영 주의사항](docs/compatibility.md) · [검증 기록](docs/verification.md)
