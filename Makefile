.PHONY: build run clean test extract

APP_NAME = xmcam-local
MAIN_FILE = main.go

build:
	@echo "Building $(APP_NAME)..."
	go build -o $(APP_NAME) $(MAIN_FILE)

run:
	@echo "Running $(APP_NAME)..."
	go run $(MAIN_FILE)

clean:
	@echo "Cleaning up..."
	rm -f $(APP_NAME)
	rm -rf VideoPlayToolSetup.exe

extract:
	@echo "Extracting web assets..."
	./extract_web_assets.sh

test:
	@echo "Running tests..."
	go test -v ./...