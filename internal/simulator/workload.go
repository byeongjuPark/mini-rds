package simulator

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// 트레픽 시뮬레이터 - DB 상태, 장애 여부에 상관 없이 1초마다 데이터를 13306으로 넣음.
func StartAppWorkload(ctx context.Context, proxyAddr, rootPass string) {
	appDSN := fmt.Sprintf("root:%s@tcp(%s)/testdb", rootPass, proxyAddr) // 접속 주소 뒤에 /testdb를 붙여 테스트 데이터베이스로 바로 접속
	appDB, err := sql.Open("mysql", appDSN)
	if err != nil {
		log.Fatalf("앱 DB 초기화 실패: %v", err)
	}

	defer appDB.Close()

	ticker := time.NewTicker(1 * time.Second) //
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var serverID int
			// 요청하는 DB에 server_id 값을 요청 쿼리
			if err := appDB.QueryRow("SELECT @@server_id").Scan(&serverID); err != nil {
				log.Printf("[App Client] 쿼리 실패 (재연결 대기 중): %v", err)
				continue
			}

			res, err := appDB.Exec("INSERT INTO heartbeat (served_by_server_id) VALUES (?)", serverID)
			if err != nil {
				log.Printf("[App Client] 쓰기 실패 (ServerID: %d): %v", serverID, err)
				continue
			}

			lastID, _ := res.LastInsertId()
			log.Printf("[App Client] 프록시(%s) 경유 INSERT 성공! -> row_id=%d, 처리 노드 ServerID=%d", proxyAddr, lastID, serverID)
		}
	}
}
