BIN := ananta
BUILD_DIR := .
CMD := ./cmd/ananta

.PHONY: build test vet run install clean cross-build android-arm64

build:
	go build -o $(BIN) $(CMD)

# Build Linux binaries for x86-64 and ARM64, plus Android ARM64 for Termux.
cross-build:
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BIN)-linux-x86_64 $(CMD)
	GOOS=linux GOARCH=arm64 go build -o $(BUILD_DIR)/$(BIN)-linux-aarch64 $(CMD)
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -o $(BUILD_DIR)/$(BIN)-android-aarch64 $(CMD)

android-arm64:
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -o $(BUILD_DIR)/$(BIN)-android-aarch64 $(CMD)

test:
	go test ./...

vet:
	go vet ./...

run: build
	./$(BIN)

install:
	go install $(CMD)

clean:
	rm -f $(BIN) $(BIN)-linux-x86_64 $(BIN)-linux-aarch64 $(BIN)-android-aarch64
