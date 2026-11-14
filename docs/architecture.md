# Go Agent 설계

## 경계와 기준선

`horrible-gh/TimeWeaver@66e99a97dacf66536ba3e108ec60a14dd517483d`의 `agent/`를 기준으로 독립 실행 파일을 구현한다. 서버의 Agent API v1/schema `1`을 유지한다. 서버와 DB에 실행 로직을 새로 추가하지 않는다. Python Agent를 수정하거나 삭제하지 않는다.

하나의 Go 모듈과 하나의 실행 파일이다. 플러그인 프레임워크, DI 컨테이너, 별도 데몬, 공용 플랫폼 계층은 두지 않는다.

## 구성

| 경로 | 책임 |
|---|---|
| `cmd/timeweaver-agent` | init/enroll/run/check/version, 시그널, 출력 |
| `internal/config` | 기존 JSON 설정과 기본값, 입력 검증 |
| `internal/protocol` | wire 모델, 스냅샷 전체 검증, 식별자/참조/필드 계약 |
| `internal/api` | HTTP, 계약 헤더, 오류 코드, 제한된 응답 크기, 리다이렉트 거부 |
| `internal/credential` | 최초 등록, 단일 동시 토큰 갱신, 인증 파일 호환 |
| `internal/securefile` | 원자적 파일 교체, OS 권한, 단일 인스턴스 잠금 |
| `internal/cron` | 7개 필드 AND 판정, 월요일=0, 60초 misfire 범위 |
| `internal/tasks` | 셸 명령, 파일 복사, ZIP, 비재귀 housekeep |
| `internal/agent` | 상태, 독립 통신 루프, 실행 컨텍스트, 결과 outbox |

업무 실행기는 HTTP를 모르고, HTTP 클라이언트는 스케줄러와 파일을 모른다. 테스트는 이 두 경계에 작은 인터페이스를 넣는다. 구체적인 동작이 없는 추상 클래스나 확장 포인트는 만들지 않는다.

## 실행 수명주기

1. 설정을 읽고 해당 인증 경로의 OS 파일 잠금을 획득한다.
2. 인증 파일이 없을 때만 환경변수의 일회용 토큰으로 등록한다. 파일이 있으면 refresh token을 사용한다.
3. heartbeat와 snapshot을 독립 루프로 실행한다. 실패한 채널만 backoff하며 다른 채널을 정지시키지 않는다.
4. 전체 스냅샷의 schema, ETag/revision, 장치, UTC 시각, cron, task, 중복 ID 및 참조를 검증한다.
5. 준비한 스냅샷을 잠금 안에서 일괄 교체한다. 변경·삭제된 그룹의 진행 중 실행은 현재 sequence까지만 완료한다.
6. 정기 작업은 그룹 중복 실행을 막고, 같은 exec_sequence의 상세 작업은 병렬로 실행한다. 수동 실행은 먼저 서버 claim을 받는다.
7. 실행 전 sequence 전체의 결과 저장 슬롯을 예약한다. 결과는 execution group별 FIFO로 보낸다.
8. 서버 ACK와 applied_transitions가 처리된 뒤에만 다음 sequence로 넘어간다. 결과 재전송은 명령 재실행과 별개다.
9. 종료 시 신규 admission을 막고 하나의 shutdown_grace 안에서 진행 중 작업과 결과 전송을 기다린다. 이후 실행 컨텍스트를 취소한다.

## 보존하는 실행 의미

`retry_count`는 수신·검증하지만 로컬 명령 재실행 횟수로 해석하지 않는다. 기존 Python 코드처럼 로컬 attempt는 1이며 서버가 오류 전이와 후속 상태를 결정한다.

크론은 year/month/day/day_of_week/hour/minute/second를 모두 만족해야 한다. day와 day_of_week를 UNIX cron의 OR로 바꾸지 않는다. 과거의 모든 시간을 재생하지 않고 60초 이내의 마지막 실행 시각으로 합친다. 기본 시간대는 호스트 로컬이며 `timezone` 설정으로 IANA 시간대를 명시할 수 있다. timezone DB를 바이너리에 포함한다.

APScheduler 전체를 복제하지 않는다. 기존 서버 계약에서 사용 가능한 정수·와일드카드·목록·범위·step 및 월/요일 이름을 처리한다. Quartz의 `?`, `L`, `W`, `#` 등은 조용히 다른 의미로 변환하지 않고 스냅샷을 거부한다.

## 인증과 안전성

access token은 메모리에만 둔다. 기존 Python과 같은 schema-1 refresh credential JSON을 원자적으로 교체한다. Unix는 0600/소유자 검사를 사용하며 Windows의 새 파일은 현재 계정/SYSTEM/Administrators ACL로 제한한다. Windows 교체에는 MoveFileExW를 사용한다.

갱신 응답을 받은 뒤 저장이 실패하면 받은 새 토큰을 메모리에 보류하고 저장을 재시도한다. 이전 refresh token으로 다시 갱신하지 않는다. 읽기 실패나 인증 거부 때문에 기존 파일을 삭제하지 않는다. 지연된 401은 그 요청에 사용된 access token만 무효화한다.

인증·장치 취소에는 fail-closed 상태를 적용한다. generation 값을 이용해 취소 전에 요청한 스냅샷 응답이 이후에 도착하여 실행을 다시 허용하지 못하게 한다. 정상 장치 상태를 다시 확인한 새 스냅샷만 실행을 복구한다.

인증·설정·로그·상태 경로는 Git에서 제외한다. 원문 HTTP 오류 본문, 토큰, 실행 명령은 일반 로그에 출력하지 않는다. API는 다른 URL로의 자동 리다이렉트를 허용하지 않는다.

## 정리 작업의 경계

housekeep는 Python의 `os.listdir` + mtime + `os.remove` 의미를 유지한다. 기간은 86400초 단위다. 실제 디렉터리의 재귀 삭제, 사용 중 파일 판정, 임의 `/tmp` 폴더 탐색은 하지 않는다. 큰 보관 일수의 duration 오버플로로 최근 파일을 삭제하지 않도록 초 단위 비교를 사용한다.

이 Agent는 OS 계정의 권한으로 명령을 실행한다. 샌드박스가 아니며 root/Administrator 실행을 요구하지 않는다.
