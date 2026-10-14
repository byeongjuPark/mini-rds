# mini-rds

### Docker 이미지 및 의존성 설치
```
# 1. MySQL 8.0 이미지 사전 다운로드
docker pull mysql:8.0

# 2. Go 모듈 초기화 및 필수 패키지 설치

go get github.com/docker/docker@v27.3.1+incompatible
go get github.com/docker/go-connections@v0.5.0
go get github.com/go-sql-driver/mysql@v1.8.1

go mod tidy
```

### 프로젝트 디렉토리 구조
```
mini-rds/
├── go.mod
├── cmd/
│   └── minirds/
│       └── main.go              # 애플리케이션 진입점 (의존성 주입 및 생명주기 관리)
└── internal/
    ├── config/
    │   └── config.go            # 클러스터 네트워크 및 포트 설정 상수
    ├── proxy/
    │   └── l4proxy.go           # L4 TCP 프록시 및 커넥션 드레이닝 (SwitchTarget)
    ├── provisioner/
    │   └── docker.go            # Docker SDK 기반 컨테이너 및 브릿지 네트워크 제어
    ├── mysqlctl/
    │   └── replication.go       # MySQL 헬스체크, GTID 복제 연결, Primary 승격 쿼리 제어
    ├── orchestrator/
    │   └── failover.go          # Primary 장애 감지 및 3단계 자동 페일오버 실행기
    └── simulator/
        └── workload.go          # 프록시 경유 실시간 INSERT 부하 생성기
```

### L4Proxy
- 바이트만 그대로 복사하여 Go의 기본 `net`과 `io`패키지로 구성