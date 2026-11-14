# TimeWeaver Agent

TimeWeaver의 독립형 Go Agent. 서버와 웹 클라이언트는 기존 `horrible-gh/TimeWeaver`를 사용합니다.

## 구현 기준

- Python Agent 기준선: `horrible-gh/TimeWeaver@66e99a97dacf66536ba3e108ec60a14dd517483d`
- 서버 Agent API v1 / schema `1` 유지
- `conf/time_weaver.json`, 기존 장치 인증 파일 및 등록 토큰 환경변수 호환
- Linux / Windows 단일 실행 파일, SpawnPoint에서 포그라운드 프로세스로 실행
- 서버/DB/웹 변경, FlowGate-Agent와의 통합, Docker 및 OS 서비스 자동 등록은 범위 밖
- 운영 중인 Python Agent와 동일 인증 정보를 사용하는 Go Agent를 동시에 실행하지 않습니다.

Go 구현과 검증 결과를 이 저장소의 `main`에 추가합니다. 기존 Python Agent는 이 저장소의 작업으로 변경하거나 중지하지 않습니다.
