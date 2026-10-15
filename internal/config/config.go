package config

const (
	NetworkName      = "mini-rds-net"
	RootPass         = "rootpass123"
	ProxyAddr        = "127.0.0.1:13306" // 클라이언트가 접속할 단일 프록시 주소
	PrimaryPort      = "13307"           // Primary 컨테이너 호스트 포트
	ReplicaPort      = "13308"           // Replica 컨테이너 호스트 포트
	PrimaryContainer = "mini-rds-primary"
	ReplicaContainer = "mini-rds-replica"
)
