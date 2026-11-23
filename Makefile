-include .env
export

SQLC ?= $(shell command -v sqlc 2>/dev/null || echo $(shell go env GOPATH)/bin/sqlc)

.PHONY: invite-admin api worker load-test dashboard up down migrate migrate-down migrate-status sqlc test test-unit test-stress demo fmt vet

up:              ## start postgres + dragonfly
	docker compose up -d

down:            ## stop containers (data kept)
	docker compose down

migrate:         ## apply migrations to DATABASE_URL
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

migrate-status:
	go run ./cmd/migrate status

sqlc:            ## regenerate internal/store from db/queries
	$(SQLC) generate

test:            ## all tests (spins up a throwaway postgres container)
	go test -race -count=1 ./...

test-unit:       ## pure unit tests, no docker
	go test -short ./...

test-stress:     ## property test with 1000 random scenarios
	go test -count=1 -run 'TestLedgerInvariants' ./internal/ledger/ -rapid.checks=1000

api:             ## run the payments API on :8080 (metrics on :9091)
	go run ./cmd/api

worker:          ## run background jobs (outbox, retries, sweeper, integrity; metrics on :9092)
	go run ./cmd/worker

load-test:       ## k6 load test against a running API (needs ADMIN_API_TOKEN)
	k6 run -e ADMIN_TOKEN=$(ADMIN_API_TOKEN) scripts/k6/load.js

dashboard:       ## run the admin dashboard on :3000
	cd dashboard && pnpm dev

invite-admin:    ## print a one-time dashboard invite link: make invite-admin EMAIL=you@x.com [ROLE=viewer]
	go run ./cmd/operator invite --email $(EMAIL) --role $(or $(ROLE),admin)

demo:            ## run the ₹1,000 payment example against the local db
	go run ./cmd/demo

fmt:
	gofmt -w .

vet:
	go vet ./...
