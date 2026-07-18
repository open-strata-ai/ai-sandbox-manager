REPO    := ai-sandbox-manager
VERSION := 1.0.0

.PHONY: build test lint run generate

build:   ## compile
	go build ./...

test:    ## run tests
	go test ./...

lint:    ## static analysis
	go vet ./...

run:     ## run locally
	go run ./cmd

generate: ## render doc/code skeletons
	python3 ../openstrata-meta/template/generate_app_skeletons.py
