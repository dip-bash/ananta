BIN := ananta

.PHONY: build test vet run install clean
build:
	go build -o $(BIN) ./cmd/ananta
test:
	go test ./...
vet:
	go vet ./...
run: build
	./$(BIN)
install:
	go install ./cmd/ananta
clean:
	rm -f $(BIN)
