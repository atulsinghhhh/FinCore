// Command migrate applies database migrations.
//
//	go run ./cmd/migrate up
//	go run ./cmd/migrate status
//	go run ./cmd/migrate down
package main

import (
	"context"
	"log"
	"os"

	"fincore/db"
)

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("DATABASE_URL is not set")
	}
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	var args []string
	if len(os.Args) > 2 {
		args = os.Args[2:]
	}
	if err := db.Migrate(context.Background(), url, command, args...); err != nil {
		log.Fatalf("migrate %s: %v", command, err)
	}
}
