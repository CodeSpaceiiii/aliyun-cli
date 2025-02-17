export VERSION=3.0.0-beta
export CLI_NAME=aliyun
export RELEASE_PATH="releases/${CLI_NAME}-cli-${VERSION}"

all: build
publish: build build_mac build_linux build_windows build_linux_arm64 gen_version

deps:
	git submodule update --init --recursive

clean:
	rm -rf out/*

build: deps
	go build -ldflags "-X 'github.com/${CLI_NAME}/${CLI_NAME}-cli/cli.Version=${VERSION}'" -o out/${CLI_NAME} main/main.go

install: build
	cp out/${CLI_NAME} /usr/local/bin

build_mac:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "-X 'github.com/${CLI_NAME}/${CLI_NAME}-cli/cli.Version=${VERSION}'" -o out/{CLI_NAME} main/main.go
	tar zcvf out/${CLI_NAME}-cli-macosx-${VERSION}-amd64.tgz -C out ${CLI_NAME}

build_linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X 'github.com/${CLI_NAME}/${CLI_NAME}-cli/cli.Version=${VERSION}'" -o out/${CLI_NAME} main/main.go
	tar zcvf out/${CLI_NAME}-cli-linux-${VERSION}-amd64.tgz -C out ${CLI_NAME}

build_windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-X 'github.com/${CLI_NAME}/${CLI_NAME}-cli/cli.Version=${VERSION}'" -o ${CLI_NAME}.exe main/main.go
	zip -r out/${CLI_NAME}-cli-windows-${VERSION}-amd64.zip ${CLI_NAME}.exe
	rm ${CLI_NAME}.exe

build_linux_arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "-X 'github.com/${CLI_NAME}/${CLI_NAME}-cli/cli.Version=${VERSION}'" -o out/${CLI_NAME} main/main.go
	tar zcvf out/${CLI_NAME}-cli-linux-${VERSION}-arm64.tgz -C out ${CLI_NAME}

gen_version:
	-rm out/version
	echo ${VERSION} >> out/version

git_release: clean build make_release_dir release_mac release_linux release_linux_arm64 release_windows

make_release_dir:
	mkdir -p ${RELEASE_PATH}

release_mac:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "-X 'github.com/${CLI_NAME}/${CLI_NAME}-cli/cli.Version=${VERSION}'" -o out/${CLI_NAME} main/main.go
	tar zcvf ${RELEASE_PATH}/${CLI_NAME}-cli-darwin-amd64.tar.gz -C out ${CLI_NAME}

release_mac_arm64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-X 'github.com/${CLI_NAME}/${CLI_NAME}-cli/cli.Version=${VERSION}'" -o out/${CLI_NAME} main/main.go
	tar zcvf ${RELEASE_PATH}/${CLI_NAME}-cli-darwin-arm64.tar.gz -C out ${CLI_NAME}

release_linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X 'github.com/${CLI_NAME}/${CLI_NAME}-cli/cli.Version=${VERSION}'" -o out/${CLI_NAME} main/main.go
	tar zcvf ${RELEASE_PATH}/${CLI_NAME}-cli-linux-amd64.tar.gz -C out ${CLI_NAME}

release_linux_arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "-X 'github.com/${CLI_NAME}/${CLI_NAME}-cli/cli.Version=${VERSION}'" -o out/${CLI_NAME} main/main.go
	tar zcvf ${RELEASE_PATH}/${CLI_NAME}-cli-linux-arm64.tar.gz -C out ${CLI_NAME}

release_windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-X 'github.com/${CLI_NAME}/${CLI_NAME}-cli/cli.Version=${VERSION}'" -o ${CLI_NAME}.exe main/main.go
	zip -r ${RELEASE_PATH}/${CLI_NAME}-cli-windows-amd64.exe.zip ${CLI_NAME}.exe
	rm ${CLI_NAME}.exe

fmt:
	go fmt ./util/... ./cli/... ./config/... ./i18n/... ./main/... ./openapi/... ./oss/... ./resource/... ./meta/...

test:
	LANG="en_US.UTF-8" go test -race -coverprofile=coverage.txt -covermode=atomic ./util/... ./cli/... ./config/... ./i18n/... ./main/... ./openapi/... ./meta/...
	go tool cover -html=coverage.txt -o coverage.html
