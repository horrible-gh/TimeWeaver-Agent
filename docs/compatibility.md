# 호환성과 전환

## 그대로 사용하는 계약

기준선은 `horrible-gh/TimeWeaver@66e99a97dacf66536ba3e108ec60a14dd517483d`이다.

- `/api/agent/v1/enroll`, `/token`, `/heartbeat`, `/snapshot`, `/manual-runs/{id}/claim`, `/executions/{id}/start`, `/executions/{id}/results`, `/events`
- `X-TW-Agent-Version`, `X-TW-Schema-Version: 1`, Bearer 인증, `If-None-Match`, 결과 `Idempotency-Key`
- schema `1` envelope와 `W/"revision 해시 앞 16자리"` ETag
- `conf/time_weaver.json`의 장치·API 설정과 `TIMEWEAVER_ENROLLMENT_TOKEN`
- 기존 refresh credential의 schema/device_id/device_name/refresh_token/refresh_token_expires_at 필드
- command/copy/archive(zip)/housekeep, `{date}`, exec_sequence, 서버 ACK 기반 오류 전이

기본 이식 기능은 서버·웹·DB 변경 없이 동작한다. 이후 추가된 재귀 housekeeping 옵션은 해당 기능을 지원하는 서버·웹·DB 마이그레이션이 필요하다. 실제 배포 서버와의 연결 검증을 마쳤다는 뜻은 아니다.

## 의도적인 차이와 한계

1. Python `conf/server.json`/LogAssist 설정은 사용하지 않는다. 일반 로그는 stderr이고 SpawnPoint가 수집한다. 상태 JSON은 기존 status_path에 쓴다. Python 상태 파일의 모든 부가 진단 필드와 동일한 형태라고 보장하지 않는다.
2. Go Agent끼리는 OS 파일 잠금으로 중복 실행을 막는다. 기존 Python Agent는 이 잠금을 모르므로 반드시 별도로 중지해야 한다.
3. 오래된 설정의 `reschedule` 필드는 읽고 보존하지만 동기화 주기는 `heartbeat_interval`/`snapshot_sync_interval`을 사용한다. 설정을 자동 재작성하지 않는다.
4. 크론의 기본 7필드 의미를 유지한다. APScheduler 전체 특수 문법이나 Quartz 문법을 지원한다고 보장하지 않는다. 유효하지 않은 표현식은 전체 스냅샷을 거부하고 마지막 정상 스냅샷을 유지한다.
5. 날짜 템플릿은 `{date}`, `{date:s}`, `{{`, `}}`를 지원한다. Python의 임의 속성 접근, 변환 플래그, 폭/정렬 지정은 지원하지 않는다. 날짜 이름은 영어이며 OS 로케일별 문자열까지 동일하다고 보장하지 않는다. 테스트된 strftime 및 기존 fallback 규칙은 유지한다.
6. 명령 stdout은 보관하지 않는다. stderr는 마지막 32 KiB로 제한하고 오류 메시지는 8,000 rune 수준으로 잘라 메모리 무제한 증가를 막는다. 기존 Python의 전체 stderr 보존과는 다르다.
7. ZIP 출력이 소스 디렉터리 내부이면 자기 자신을 압축하는 것을 막기 위해 거부한다. 특수 장치 파일의 복사/압축도 거부한다.
8. 환경 진단의 IP는 로컬 인터페이스에서 구한다. hostname DNS 조회 때문에 Agent 시작이 막히지 않게 하며, Python의 hostname 해석 IP와 다를 수 있다.
9. 결과 outbox는 기존처럼 메모리 기반이다. 강제 종료·호스트 장애 시 미전송 결과가 유실될 수 있다. 재시작을 넘어선 exactly-once 실행/전송을 보장하지 않는다. 정상 종료는 grace 안에서 배출을 시도하고 잔여 수를 경고한다.
10. Windows 파일 ACL과 프로세스 트리 종료 구현은 포함하지만 Linux에서의 cross-build만으로 Windows 실행 검증을 대신하지 않는다. Windows 런타임 및 실제 SpawnPoint 전환은 확인이 필요하다.

## 운영 전환 순서

1. 기존 Agent에서 실행·결과 전송이 끝났는지 확인하고 중지한다.
2. 기존 OS 계정과 작업 디렉터리를 유지한다. 새 Go 바이너리의 절대 경로와 `run --config conf/time_weaver.json`을 SpawnPoint에 지정한다.
3. `check`로 설정·인증 파일을 점검한다. 유효한 credential이 있으면 일회용 토큰을 다시 전달하지 않는다.
4. 동일 장치 ID의 heartbeat 및 HEALTHY 상태, 정기 실행 1건, 수동 실행 1건, 결과 이력, 재시작 후 인증 재사용을 확인한다.
5. 실패 시 Go Agent를 먼저 중지한 후 Python Agent로 복귀한다. Go가 갱신한 최신 인증 파일을 그대로 사용하며, 이전 refresh token 백업을 덮어쓰지 않는다.

작업 디렉터리까지 바꾸려면 설정의 상대 경로와 파일 소유권을 별도로 확인한다. 인증 파일은 내용이 맞더라도 다른 계정의 Unix 권한 또는 Windows ACL 때문에 읽지 못할 수 있다.

## 정리 기능 주의

housekeep의 기본값은 비재귀이며, 대상 디렉터리 바로 아래 오래된 파일을 삭제한다. 이 기존 모드에서는 오래된 실제 디렉터리를 만나면 실패할 수 있다. 서버의 **하위 폴더 포함**을 켜면 파일별 수정 시각으로 모든 깊이의 오래된 일반 파일을 정리한다. 디렉터리 자체, 심볼릭 링크, Windows junction/reparse point, 특수 파일은 재귀 모드에서 건드리지 않는다.

Go 에이전트를 먼저 업데이트·재시작하고 서버와 웹을 업데이트한다. 재등록은 필요 없다. 서버는 `housekeep_recursive_v1` 지원을 확인한 뒤 옵션 저장과 스냅샷 전달을 허용한다. Python 에이전트는 이 옵션을 지원하지 않으므로 비활성 상태로 유지하거나 Go로 전환한다.

사용 중 파일 판정이나 적대적인 동시 경로 교체 방어는 제공하지 않는다. `/tmp` 전체가 아닌 에이전트 계정이 관리하는 명시적인 정리 경로를 사용한다.
