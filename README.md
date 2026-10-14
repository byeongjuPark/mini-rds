# mini-rds
```
# 1. MySQL 8.0 이미지 사전 다운로드
docker pull mysql:8.0

# 2. Go 모듈 초기화 및 필수 패키지 설치
mkdir mini-rds && cd mini-rds
go mod init mini-rds

go get github.com/docker/docker@v27.3.1+incompatible
go get github.com/docker/go-connections@v0.5.0
go get github.com/go-sql-driver/mysql@v1.8.1

go mod tidy
```