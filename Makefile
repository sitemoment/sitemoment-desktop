# SiteMoment Desktop: common commands for local development.
#
#   make dev        run the app with hot reload (test data in $(DATA_DIR))
#   make run        build once and run the binary
#   make app        build the unsigned macOS .app / .dmg into bin/release
#   make test       Go and frontend tests
#   make reset      delete the test data directory
#
# Google (Search Console, GA4) needs a "Desktop app" OAuth client. Put it in
# .env.local (not committed):
#
#   SITEMOMENT_GOOGLE_CLIENT_ID=...
#   SITEMOMENT_GOOGLE_CLIENT_SECRET=...

-include .env.local
export

WAILS    ?= $(shell command -v wails3 2>/dev/null || echo $(HOME)/go/bin/wails3)
DATA_DIR ?= /tmp/smd-test
VERSION  ?= 0.0.0-dev

export SITEMOMENT_DATA_DIR := $(DATA_DIR)

.PHONY: help setup bindings dev run app test reset

help:
	@sed -n '3,7p' Makefile | sed 's/^# //'

setup:
	@command -v $(WAILS) >/dev/null || go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
	cd frontend && npm install
	$(MAKE) bindings

bindings:
	cd frontend && npm run bindings

dev: setup
	@echo "data: $(DATA_DIR)"
	$(WAILS) dev

run: setup
	cd frontend && npm run build
	go build -o bin/sitemoment-dev .
	@echo "data: $(DATA_DIR)"
	./bin/sitemoment-dev

app: setup
	VERSION=$(VERSION) $(WAILS) task darwin:release
	@echo "open bin/release/SiteMoment-macos-universal.dmg"

test: setup
	cd frontend && npm test && npm run build
	go vet ./...
	go test ./...

reset:
	rm -rf $(DATA_DIR)
