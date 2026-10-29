VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN     := bin
CMDS    := panel agent genconf
PROTOS  := $(wildcard proto/*.proto)

# Our own sing-box build: the official release tags (release/DEFAULT_BUILD_TAGS_OTHERS)
# plus with_v2ray_api, without which there are no per-user traffic statistics. The version is pinned
# in build/sing-box/go.mod; to update: cd build/sing-box && go get -tool github.com/sagernet/sing-box/cmd/sing-box@vX.Y.Z
# The same tags and ldflags are in .goreleaser.yaml.
SINGBOX_DIR     := build/sing-box
SINGBOX_VERSION := $(shell cd $(SINGBOX_DIR) && go list -m -f '{{.Version}}' github.com/sagernet/sing-box)
SINGBOX_TAGS    ?= with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,badlinkname,tfogo_checklinkname0,with_v2ray_api
SINGBOX_LDFLAGS := -X 'github.com/sagernet/sing-box/constant.Version=$(SINGBOX_VERSION:v%=%)' -X runtime.godebugDefault=multipathtcp=0,tlssha1=1 -checklinkname=0 -s -w -buildid=
SINGBOX_PKG     := github.com/sagernet/sing-box/cmd/sing-box
SINGBOX_FLAGS    = -trimpath -tags "$(SINGBOX_TAGS)" -ldflags "$(SINGBOX_LDFLAGS)"
HOSTOS   := $(shell go env GOHOSTOS)
HOSTARCH := $(shell go env GOHOSTARCH)
GOOS     ?= $(HOSTOS)
GOARCH   ?= $(HOSTARCH)

.PHONY: all build test lint proto clean sing-box agent deb

all: build

build:
	@mkdir -p $(BIN)
	@for c in $(CMDS); do \
		echo "go build $$c"; \
		CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN)/$$c ./cmd/$$c || exit 1; \
	done

# Agent only, for a node: make agent GOOS=linux GOARCH=amd64 -> bin/agent-linux-amd64
agent:
	@mkdir -p $(BIN)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags "$(LDFLAGS)" \
		-o $(BIN)/agent-$(GOOS)-$(GOARCH) ./cmd/agent

test:
	go test ./...

lint:
	golangci-lint run ./...

# protoc: pip install grpcio-tools (python3 -m grpc_tools.protoc) or the system protoc;
# plugins: go install google.golang.org/protobuf/cmd/protoc-gen-go google.golang.org/grpc/cmd/protoc-gen-go-grpc
PROTOC ?= $(shell command -v protoc 2>/dev/null || echo "python3 -m grpc_tools.protoc")

proto:
	$(PROTOC) -Iproto --go_out=. --go_opt=module=github.com/artemlavr/giocondo \
		--go-grpc_out=. --go-grpc_opt=module=github.com/artemlavr/giocondo \
		$(PROTOS)

# Host platform -> bin/sing-box. For a node from a Mac: make sing-box GOOS=linux GOARCH=amd64
# -> bin/sing-box-linux-amd64.
sing-box:
	@mkdir -p $(BIN)
	@set -e; \
	if [ "$(GOOS)/$(GOARCH)" = "$(HOSTOS)/$(HOSTARCH)" ]; then out=sing-box; else out=sing-box-$(GOOS)-$(GOARCH); fi; \
	cd $(SINGBOX_DIR) && GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		go build $(SINGBOX_FLAGS) -o $(abspath $(BIN))/$$out $(SINGBOX_PKG); \
	echo "built $(BIN)/$$out ($(SINGBOX_VERSION))"

# deb packages (snapshot) into dist/ via GoReleaser; requires a git commit. GORELEASER can be
# overridden, e.g. with docker run ... goreleaser/goreleaser.
GORELEASER ?= go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)
GORELEASER_VERSION ?= v2.19.0

deb:
	SINGBOX_VERSION=$(SINGBOX_VERSION:v%=%) $(GORELEASER) release --snapshot --clean

clean:
	rm -rf $(BIN)
