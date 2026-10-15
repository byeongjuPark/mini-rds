package mysqlctl

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql" // MySQL 드라이버
)

// 접속 주소 정보와 노드(dsn) 이름을 받아, 성공 시 DB 커넥션 풀 객체(*sql.DB)와 에러 여부를 반환
func WaitForMySQL(dsn, nodeName string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn) // 드라이버를 사용해 DB 관리 객체 생성
	if err != nil {
		return nil, err
	}

	// 40번 시도하며, 1.5초 간격으로 Ping()을 통해 MySQL 데몬 준비 여부 확인
	for i := 0; i < 40; i++ {
		if err := db.Ping(); err == nil {
			log.Printf("[MySQLCtl] %s 데몬 준비 완료", nodeName)
			return db, nil
		}
		time.Sleep(1500 * time.Millisecond)
	}
	return nil, fmt.Errorf("%s 준비 시간 초과", nodeName)
}

// GTID 복제 파이프라인 연결
func ConfigureReplication(primaryDB, replicaDB *sql.DB, primaryHost, rootPass string) error {
	if _, err := primaryDB.Exec(`CREATE DATABASE IF NOT EXISTS testdb`); err != nil { // testdb 데이터베이스 생성
		return err
	}
	// heartbeat 테이블 생성
	_, err := primaryDB.Exec(`
						CREATE TABLE IF NOT EXISTS testdb.heartbeat (
							id INT AUTO_INCREMENT PRIMARY KEY,
							served_by_server_id INT,
							created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
						);
	`)
	if err != nil {
		return err
	}

	_, _ = replicaDB.Exec(`STOP REPLICA;`) // 복제 중지
	// 누구를 primary로 삼을지, 어떤 계정으로 복제할지, GTID 기반 복제를 사용할지 설정
	// SOURCE_AUTO_POSITION=1 - GTID 기반 복제 사용, 0은 binlog 기반 복제,
	// GTID 기반 복제로 어느 노드가 primary인지 자동으로 판단 가능(어느 노드가 최신 데이터까지 복제 받았는지 판단)
	query := fmt.Sprintf(`
				CHANGE REPLICATION SOURCE TO
					SOURCE_HOST='%s',
					SOURCE_PORT=3306,
					SOURCE_USER='root',
					SOURCE_PASSWORD='%s',
					SOURCE_AUTO_POSITION=1,
					GET_SOURCE_PUBLIC_KEY=1;
				`, primaryHost, rootPass)

	if _, err := replicaDB.Exec(query); err != nil {
		return fmt.Errorf("복제 설정 실패: %w", err)
	}

	if _, err := replicaDB.Exec(`START REPLICA;`); err != nil {
		return fmt.Errorf("복제 시작 실패: %w", err)
	}

	log.Println("[MySQLCtl] GTID 기반 Primary -> Replica 복제 파이프라인 연결 완료")

	return nil
}

// Replica를 Primary로 승격시키는 함수
func PromoteToPrimary(replicaDB *sql.DB) error {

	// 승격 시 필요한 쿼리들을 순차적으로 실행
	promoteQueries := []string{
		"STOP REPLICA;",                     // 복제 중지
		"RESET REPLICA ALL;",                // 복제 설정 초기화
		"SET GLOBAL super_read_only = OFF;", // super_read_only 설정 해제
		"SET GLOBAL read_only = OFF;",       // read_only 설정 해제
	}

	for _, q := range promoteQueries { // 승격 쿼리 실행
		if _, err := replicaDB.Exec(q); err != nil {
			return fmt.Errorf("프로모션 실패: %w", err)
		}
	}
	return nil
}
