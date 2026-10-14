package proxy

import (
	"context"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// L4Proxy 구조체
type L4Proxy struct {
	listenAddr  string                // 프록시가 클라이언트 접속을 기다릴 주소
	mu          sync.RWMutex          // 고루틴이 동시 변수 접근 시 방지
	targetAddr  string                // 데이터를 전당할 백엔드 MySQL의 주소
	activeConns map[net.Conn]struct{} // 현재 연결된 네티워크 연결 목록 맵
}

// L4Proxy 생성함수
func NewL4Proxy(listenAddr, initialTarget string) *L4Proxy {
	return &L4Proxy{
		listenAddr:  listenAddr,
		targetAddr:  initialTarget,
		activeConns: make(map[net.Conn]struct{}),
	}
}

// 서버 시작
func (p *L4Proxy) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", p.listenAddr) // TCP 포트 개방
	if err != nil {
		return err
	}

	go func() { // 바로 실행 익명 함수(GO런타임이 비동기 경량 스레드에서 별도 실행, 메인 흐름은 멈추지 않고 바로 다음 줄로 넘어감)
		<-ctx.Done()     // 프로그램 종료 신호가 오면 열리는 채널, 종료 신호가 나올 때까지 대기
		listener.Close() // TCP 포트 닫기
	}()

	log.Printf("[L4Proxy] 라우터 시작 : %s -> 초기 타겟(%s)", p.listenAddr, p.GetTarget())

	for {
		clientConn, err := listener.Accept() // 클라이언트가 접속할 때까지 대기
		if err != nil {
			select {
			case <-ctx.Done(): // 프로그램 종료 신호에 의해 listener가 닫혀서 난 정상적인 종료
				return nil // 문제없음 반환 후 루프 종료
			default:
				continue // 종료 신호 없고, 접속 수락 실패한 경우 다시 다음 클라이언트 접속할 때까지 대기
			}
		}
		go p.handleConnection(clientConn) // 접속 성공한 클라이언트 데이터 처리(백그라운드 처리)
	}
}

// 타겟을 읽기 전에 Read Rock을 걸어 주소 보호
func (p *L4Proxy) GetTarget() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.targetAddr
}

// 라우팅 타겟을 변경하고 기존 연결을 모두 끊고, 재연결 시도
func (p *L4Proxy) SwitchTarget(newTarget string) {
	p.mu.Lock()
	// 기존 주소를 백업(oldTarget)해 두고, 타겟 주소를 새 DB 주소(newTarget)로 덮음
	oldTarget := p.targetAddr
	p.targetAddr = newTarget

	connsToClose := make([]net.Conn, 0, len(p.activeConns)) // 끊어야 할 기존 연결들을 따로 담아둘 슬라이스 동적 할당

	for c := range p.activeConns { // 기존 클라이언트 추출
		connsToClose = append(connsToClose, c)
	}
	p.mu.Unlock()

	for _, c := range connsToClose { // 추출한 기존 클라이언트 끊기, 끊긴 클라이언트는 즉시 재연결 시도하고 newTarget으로 연결됨
		c.Close()
	}

	log.Printf("[L4Proxy] 라우팅 스위칭 완료 (%s -> %s, 기존 연결 %d개 강제 종료)", oldTarget, newTarget, len(connsToClose))
}

// 양방향 데이터 중계, 클라이언트 1명과 백엔드 MySQL 1대를 실제로 연결해 데이터를 양쪽으로 복사
func (p *L4Proxy) handleConnection(clientConn net.Conn) {
	target := p.GetTarget()                                           // 백엔드 MySQL 주소 가져오기
	backendConn, err := net.DialTimeout("tcp", target, 2*time.Second) // 프록시가 MySQL로 Dial을 걸어 TCP 연결, (timeout 2s)
	if err != nil {
		clientConn.Close() //MySQL이 죽어있어서 연결 실패 시, 클라이언트 연결도 닫음
		return
	}

	p.mu.Lock()
	p.activeConns[clientConn] = struct{}{} // activeConns 클라이언트 등록
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.activeConns, clientConn)
		p.mu.Unlock()

		clientConn.Close()
		backendConn.Close()
	}()

	done := make(chan struct{}, 2) // 작업이 끝났음을 알리는 신호용 채널
	go func() {                    // 클라이언트 -> DB 방ㅎ
		_, _ = io.Copy(backendConn, clientConn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, backendConn)
		done <- struct{}{}
	}()
	<-done
}
