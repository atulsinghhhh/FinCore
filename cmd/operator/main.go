// Command operator bootstraps dashboard access from the server side.
//
//	go run ./cmd/operator invite --email you@company.com --role admin
//
// It prints a one-time invite link. Use it for the first admin; after that,
// admins invite everyone else from the dashboard's Team page.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"fincore/internal/operators"
	"fincore/internal/platform"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "invite" {
		fmt.Fprintln(os.Stderr, "usage: operator invite --email <email> [--role admin|viewer]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("invite", flag.ExitOnError)
	email := fs.String("email", "", "email address to invite")
	role := fs.String("role", operators.RoleAdmin, "admin or viewer")
	dashboard := fs.String("dashboard-url", envOr("DASHBOARD_URL", "http://localhost:3100"), "dashboard base URL for the link")
	_ = fs.Parse(os.Args[2:])

	ctx := context.Background()
	cfg, err := platform.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := platform.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	inv, token, err := operators.NewService(pool).Invite(ctx, nil, *email, *role)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Invited %s as %s (expires %s).\nOpen this link to set a password:\n\n  %s/invite/%s\n\n", inv.Email, inv.Role, inv.ExpiresAt.Format("2 Jan 2006 15:04 MST"), *dashboard, token)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
