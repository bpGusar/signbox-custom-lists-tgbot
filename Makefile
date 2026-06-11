BINARY := lst-signbox-lists-tgbot
PKG := ./cmd/lst-signbox-lists-tgbot

.PHONY: build test tidy clean cross ipk ipk-aarch64

OPENWRT_VERSION ?= 24.10.5
IPK_TARGETS ?= aarch64_cortex-a53 arm_cortex-a7_neon-vfpv4 mipsel_24kc x86_64

build:
	go build -trimpath -ldflags="-s -w" -o $(BINARY) $(PKG)

test:
	go test ./...

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)

# Local cross-compile examples (adjust GOARCH for your router)
cross-arm:
	GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY)-arm $(PKG)

cross-aarch64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY)-aarch64 $(PKG)

cross-mipsle:
	GOOS=linux GOARCH=mipsle GOMIPS=softfloat CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY)-mipsle $(PKG)

# Build OpenWrt .ipk packages (requires Docker)
ipk:
	OPENWRT_VERSION=$(OPENWRT_VERSION) TARGETS="$(IPK_TARGETS)" ./scripts/build-ipk.sh

ipk-aarch64:
	OPENWRT_VERSION=$(OPENWRT_VERSION) TARGETS=aarch64_cortex-a53 ./scripts/build-ipk.sh
