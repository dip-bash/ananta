BIN := ananta
BUILD_DIR := .

.PHONY: build test vet run install clean cross-build

build:
	go build -o $(BIN) ./cmd/ananta

cross-build:
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BIN)-linux-x86_64 ./cmd/ananta
	GOOS=linux GOARCH=arm64 go build -o $(BUILD_DIR)/$(BIN)-linux-aarch64 ./cmd/ananta

test:
	go test ./...

vet:
	go vet ./...

run: build
	./$(BIN)

install:
	go install ./cmd/ananta

clean:
	rm -f $(BIN) $(BIN)-linux-x86_64 $(BIN)-linux-aarch64
