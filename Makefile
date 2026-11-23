BIN := tst
BUILD_DIR := .
CMD := ./cmd/tst

.PHONY: build test vet run install clean cross-build android-arm64

build:
	go build -o $(BIN) $(CMD)

# Build Linux binaries for x86-64 and ARM64, plus Android ARM64 for Termux.
cross-build:
	GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/tstrip-linux-x86_64 ./cmd/tst
	GOOS=linux GOARCH=arm64 go build -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/tstrip-linux-aarch64 ./cmd/tst
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/tstrip-android-aarch64 ./cmd/tst/

android-arm64:
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -o $(BUILD_DIR)/tstrip-android-aarch64 $(CMD)

test:
	go test ./...

vet:
	go vet ./...

run: build
	./$(BIN)

install:
	go install $(CMD)

clean:
	rm -f $(BIN) tstrip-linux-x86_64 tstrip-linux-aarch64 tstrip-android-aarch64
