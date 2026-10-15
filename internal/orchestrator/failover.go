package orchestrator

import (
	"context"
	"database/sql"
	"log"
	"time"

	"mini-rds/internal/mysqlctl"
	"mini-rds/internal/provisioner"
	"mini-rds/internal/proxy"
)

// 오케스트레이터가 장애 복구를 위해 사용할 도구를 Main 루프에서 포인터를 받아 구조체로 묶어 관리
type FailOverOrchestrator struct {
	prov             *provisioner.DockerProvisioner // 죽은 컨테이너를 강제 정지(Fencing)시킬 때 쓸 Docker 제어기
	primaryDB        *sql.DB                        // 생존 확인 용 Primary DB 커넥션 풀
	replicaDB        *sql.DB                        // Replica DB 커넥션 풀
	l4Proxy          *proxy.L4Proxy                 // 장애 시 라우팅 방향을 바꿀 L4 Proxy
	primaryContainer string                         // 현재 Primary로 작동 중인 컨테이너 ID(정지시킬)
	newTargetAddr    string                         // 장애 시 프록시가 새로 바라볼 Replica 컨테이너 주소
}

func NewFailOverOrchestrator(prov *provisioner.DockerProvisioner, primaryDB, replicaDB *sql.DB, l4Proxy *proxy.L4Proxy, primaryContainer, newTargetAddr string) *FailOverOrchestrator {
	return &FailOverOrchestrator{
		prov:             prov,
		primaryDB:        primaryDB,
		replicaDB:        replicaDB,
		l4Proxy:          l4Proxy,
		primaryContainer: primaryContainer,
		newTargetAddr:    newTargetAddr,
	}
}

// 1초 주기 헬스체크, 타임아웃 제어
func (o *FailOverOrchestrator) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	failCount := 0
	failedOver := false

	// 두 개의 채널 중 먼저 오는 신호를 기다림(ctx.Done(), ticker.C)
	for {
		select {
		case <-ctx.Done(): // 프로그램 종료 신호
			return
		case <-ticker.C:
			if failedOver { // 페일오버가 이미 발생했으면 헬스체크 루프 종료
				continue
			}

			pingCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond) // Primary DB Ping() 타임아웃 제어, 800ms 이상 걸리면 취소
			err := o.primaryDB.PingContext(pingCtx)                           // DB에 생존 확인 Ping() 요청, 타임아웃 발생 시 err 반환
			cancel()                                                          // WithTimeout()로 생성한 Context는 사용 후 반드시 cancel() 호출하여 리소스 해제

			// failCount 3번 누적 시 장애로 판단하고 페일오버 절차 진행 (일시적인 네트워크 장애 고려)
			if err != nil {
				failCount++
				log.Printf("[Orchestrator] Primary 응답 없음! (연결 실패: %d/3)", failCount)
				if failCount >= 3 {
					log.Println("[Orchestrator] Primary 장애 감지! Failover 시작...")
					o.executeFailover(ctx)
					failedOver = true
				}
			} else {
				failCount = 0
			}
		}
	}
}

/*
FAILOVER 절차
1. 기존 Primary 컨테이너 펜싱
2. Replica를 Primary로 승격
3. L4 Proxy 라우팅 전환 및 기존 연결 끊기
*/
func (o *FailOverOrchestrator) executeFailover(ctx context.Context) {
	log.Printf("[Failover 1/3] 기존 Primary(%s) 펜싱(Fencing) 중... \n", o.primaryContainer)
	_ = o.prov.FenceContainer(ctx, o.primaryContainer)

	log.Println("[Failover 2/3] Replica를 Primary로 승격(Promotion) 중...")
	if err := mysqlctl.PromoteToPrimary(o.replicaDB); err != nil {
		log.Fatalf("[Failover] Replica 승격 실패: %v", err)
	}

	log.Println("[Failover 3/3] L4 Proxy 라우팅 전환 및 기존 연결 끊기 중...")
	o.l4Proxy.SwitchTarget(o.newTargetAddr)

}
