package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mini-rds/internal/config"
	"mini-rds/internal/mysqlctl"
	"mini-rds/internal/orchestrator"
	"mini-rds/internal/provisioner"
	"mini-rds/internal/proxy"
	"mini-rds/internal/simulator"
)

/*
	1. Docker 프로비저너 초기화 및 네트워크 생성
	2. Primary / Replica 컨테이너 가동
	3. MySQL 부팅 대기 및 GTID 복제 구성
	4. L4 TCP 프록시 가동
	5. 오케스트레이터 및 워크로드 시뮬레이터 시작
*/

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// 1. Docker 프로비저너 초기화 및 네트워크 생성
	prov, err := provisioner.NewDockerProvisioner(config.NetworkName, config.RootPass)
	if err != nil {
		log.Fatalf("프로비저너 초기화 실패: %v", err)
	}

	log.Println("[Mini-RDS] 인프라 프로비저닝을 시작...")
	_ = prov.EnsureNetwork(ctx)

	// 2. Primary / Replica 컨테이너 가동
	primarySpec := provisioner.NodeSpec{
		Name: config.PrimaryContainer, HostPort: config.PrimaryPort, ServerID: 101, ReadOnly: false,
	}
	replicaSpec := provisioner.NodeSpec{
		Name: config.ReplicaContainer, HostPort: config.ReplicaPort, ServerID: 102, ReadOnly: true,
	}

	if err := prov.StartMySQLContainer(ctx, primarySpec); err != nil {
		log.Fatalf("Primary 시작 실패 %v", err)
	}
	if err := prov.StartMySQLContainer(ctx, replicaSpec); err != nil {
		log.Fatalf("Replica 시작 실패: %v", err)
	}

	// 3. MySQL 부팅 대기 및 GTID 복제 구성
	log.Println(" * MySQL 컨테이너 초기화를 기다리는 중 ... ")
	primaryDSN := fmt.Sprintf("root:%s@tcp(127.0.0.1:%s)/", config.RootPass, config.PrimaryPort)
	replicaDSN := fmt.Sprintf("root:%s@tcp(127.0.0.1:%s)/", config.RootPass, config.ReplicaPort)

	primaryDB, err := mysqlctl.WaitForMySQL(primaryDSN, "Primary(101)")
	if err != nil {
		log.Fatal(err)
	}
	replicaDB, err := mysqlctl.WaitForMySQL(replicaDSN, "Replica(102)")
	if err != nil {
		log.Fatal(err)
	}

	if err := mysqlctl.ConfigureReplication(primaryDB, replicaDB, primarySpec.Name, config.RootPass); err != nil {
		log.Fatal(err)
	}

	// 4. L4 TCP 프록시 가동
	l4Proxy := proxy.NewL4Proxy(config.ProxyAddr, "127.0.0.1:"+config.PrimaryPort)
	go func() {
		if err := l4Proxy.Start(ctx); err != nil {
			log.Fatalf("프록시 오류: %v, err")
		}
	}()
	time.Sleep(500 * time.Millisecond)

	// 5. 오케스트레이터 및 워크로드 시뮬레이터 시작
	orch := orchestrator.NewFailOverOrchestrator(
		prov, primaryDB, replicaDB, l4Proxy,
		config.PrimaryContainer, "127.0.0.1:"+config.ReplicaPort,
	)
	go orch.Start(ctx)
	go simulator.StartAppWorkload(ctx, config.ProxyAddr, config.RootPass)

	log.Println("================================================================================")
	log.Println("Mini-RDS 클러스터 가동 완료!")
	log.Println("다른 터미널을 열고 아래 명령어로 Primary 장애를 일으켜 보세요:")
	log.Printf("docker kill %s\n", config.PrimaryContainer)
	log.Println("================================================================================")

	<-ctx.Done()
	log.Println("\n 종료 시그널 수신: 컨테이너 및 네트워크 정리 ... ")
	cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prov.Cleanup(cleanCtx, config.PrimaryContainer, config.ReplicaContainer)
}
