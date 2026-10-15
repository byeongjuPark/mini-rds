package provisioner

import (
	"context"
	"fmt"
	"log"

	"github.com/docker/docker/api/types/container" // container
	"github.com/docker/docker/api/types/network"   // network
	"github.com/docker/docker/client"              // client
	"github.com/docker/go-connections/nat"         // nat
)

type NodeSpec struct { // MySQL 노드 컨테이너를 기동하기 위한 스펙 구조체
	Name     string
	HostPort string
	ServerID int
	ReadOnly bool
}

type DockerProvisioner struct { // Docker 데몬과 상호작용하여 MySQL 컨테이너를 기동하고 관리하는 구조체
	cli         *client.Client
	networkName string
	rootPass    string
}

// 프로비저너 생성과 네트워크 준비
func NewDockerProvisioner(networkName, rootPass string) (*DockerProvisioner, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation()) // Docker SDK 클라이언트 생성, 환경변수 기반, API 버전 협상
	if err != nil {
		return nil, err
	}
	return &DockerProvisioner{
		cli:         cli,
		networkName: networkName,
		rootPass:    rootPass,
	}, nil
}

// Docker 네트워크를 생성, 기존 네트워크가 있으면 제거 후 새로 생성
func (d *DockerProvisioner) EnsureNetwork(ctx context.Context) error {
	_ = d.cli.NetworkRemove(ctx, d.networkName)
	_, err := d.cli.NetworkCreate(ctx, d.networkName, network.CreateOptions{ // 컨테이너끼리 이름으로 통신할 수 있는 브리지 네트워크 생성
		Driver: "bridge",
	})
	return err
}

// MySQL 컨테이너를 기동, 기존 컨테이너가 있으면 제거 후 새로 생성
func (d *DockerProvisioner) StartMySQLContainer(ctx context.Context, spec NodeSpec) error {
	_ = d.cli.ContainerRemove(ctx, spec.Name, container.RemoveOptions{Force: true}) // 기존 컨테이너 제거, Force 옵션으로 강제 제거

	readOnlyVal := "OFF"
	if spec.ReadOnly {
		readOnlyVal = "ON"
	}

	cmd := []string{ // MySQL 컨테이너 기동 시 전달할 명령어 및 실행 옵션
		fmt.Sprintf("--server-id=%d", spec.ServerID),
		"--log-bin=mysql-bin",
		"--gtid-mode=ON",
		"--enforce-gtid-consistency=ON",
		"--binlog-format=ROW",
		"--log-replica-updates=ON",
		fmt.Sprintf("--read-only=%s", readOnlyVal),
		fmt.Sprintf("--super-read-only=%s", readOnlyVal), // Replica에서 실수로 쓰기 요청이 들어와도 DB 엔진에서 차단
	}

	// 컨테이너 내부 3306 포트를 호스트의 포트와 연결하기 위한 포트 바인딩 설정, nat.PortMap 타입 사용
	portBinding := nat.PortMap{
		"3306/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: spec.HostPort}},
	}

	resp, err := d.cli.ContainerCreate(ctx,
		// 컨테이너 내부 환경 설정
		&container.Config{
			Image: "mysql:8.0",
			Env:   []string{"MYSQL_ROOT_PASSWORD=" + d.rootPass},
			Cmd:   cmd, // 실행 옵션
			ExposedPorts: nat.PortSet{
				"3306/tcp": struct{}{},
			},
		},
		// 컨테이너 외부(호스트) 환경 설정
		&container.HostConfig{
			PortBindings: portBinding,
		},
		// 브릿지 네트워크 설정, 컨테이너가 생성될 때 네트워크에 연결되도록 함
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				d.networkName: {},
			},
		},
		nil,       // 플랫폼(CPU 아키텍처 등) 설정 - 기본값 nil
		spec.Name, // 생성할 컨테이너 이름
	)
	if err != nil {
		return err
	}

	log.Printf("[Provisioner] 컨테이너 기동 중: %s (포트: %s, ServerID: %d)", spec.Name, spec.HostPort, spec.ServerID)

	// ** Docker API는 컨테이너를 만드는 것(ContainerCreate)과 켜는 것(ContainerStart)이 별개 **

	// 컨테이너를 실제로 시작
	return d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{})
}

// 장애가 발생한 노드를 격리하기 위해 컨테이너를 즉시 정지
func (d *DockerProvisioner) FenceContainer(ctx context.Context, containerName string) error {
	return d.cli.ContainerStop(ctx, containerName, container.StopOptions{})
}

// 컨테이너와 네트워크를 정리, 종료 시 호출
func (d *DockerProvisioner) Cleanup(ctx context.Context, containerNames ...string) {
	for _, name := range containerNames {
		_ = d.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	}

	_ = d.cli.NetworkRemove(ctx, d.networkName)
}
