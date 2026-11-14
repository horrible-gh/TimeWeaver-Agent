# 검증 기록

검증일: 2026-11-14. 실행 환경: Linux amd64, Go 1.23.2.
기준 Python Agent: `horrible-gh/TimeWeaver@66e99a97dacf66536ba3e108ec60a14dd517483d`.

## 실행한 검증

| 검증 | 결과 |
|---|---|
| `go test -race -count=3 ./...` | 9개 패키지 모두 통과 |
| `go vet ./...` | 통과 |
| Linux amd64, `CGO_ENABLED=0` build | 통과 |
| Linux arm64, `CGO_ENABLED=0` cross-build | 통과 |
| Windows amd64, `CGO_ENABLED=0` cross-build | 통과 |
| `bash -n scripts/build.sh` 및 `bash scripts/build.sh --no-pull` | 통과 |
| Linux 실행 파일 `version`, `--help`, 임시 작업 폴더에서 `init` / `check` | 통과. 인증 없는 상태를 `missing`으로 표시 |

## 자동 테스트 범위

- 기존 wire schema, 장치 identity, UTC/ETag, 잘못된 스냅샷 전체 거부 및 마지막 정상 상태 유지
- 크론 7필드, 월요일=0, AND 조합, DST 중복 시간, 60초 misfire 범위
- 등록 후 인증 재사용, 동시 refresh 단일 호출, 갱신 정보 저장 실패 후 재저장, 늦은 401 응답 경합
- private credential 원자 저장, 중복 Go Agent 잠금, 심볼릭 링크 경로 거부
- 명령 실행/취소, 제한된 stderr, 복사/ZIP, 날짜 템플릿, 비재귀 housekeep
- 결과 FIFO, 동일 idempotency key 재전송, 예약 용량 한도, 영구 거절, 다음 sequence의 ACK 장벽
- 실행 중 스냅샷 변경, 수동 claim 중복 방지, 인증 취소 이전 스냅샷 응답 차단
- HTTP 모의 서버와 실제 파일 복사를 사용한 전체 실행: 최초 등록 → heartbeat/snapshot → 실행 → 503 결과 재시도 → 종료 → 재시작 시 refresh

## 아직 수행하지 않은 검증

실제 운영 TimeWeaver 서버 접속, 운영 장치 교체, Windows에서의 실행·ACL·프로세스 트리 종료, Linux arm64 실기 실행은 수행하지 않았다. Cross-build 성공은 해당 OS에서의 동작 검증을 의미하지 않는다.

GitHub Actions workflow는 Linux/Windows 테스트를 정의하지만, 이 기록은 로컬 검증 결과다. 실제 Actions 실행 여부와 결과는 저장소의 실행 기록으로 별도 확인한다. 기존 Python Agent의 전체 테스트를 기계적으로 변환한 것은 아니며, 의도적인 차이는 `compatibility.md`에 명시한다.
