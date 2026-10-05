// Command heraldctl provisions applications and producer credentials.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Omnific-Tech/herald/internal/config"
	"github.com/Omnific-Tech/herald/internal/crypto"
	"github.com/Omnific-Tech/herald/internal/store/postgres"
)

const usage = `heraldctl provisions Herald applications and producer tokens.

Usage:
  heraldctl keygen
  heraldctl hash-password
  heraldctl migrate
  heraldctl create-application -slug <slug> -name <name> -origin <url> -subject <mailto:…>
  heraldctl list-applications
  heraldctl create-producer -application <slug> -name <name>
  heraldctl revoke-producer -application <slug> -name <name>
  heraldctl list-producers
  heraldctl rewrap

DATABASE_URL is required by every command except keygen and hash-password. ENCRYPTION_KEY is
required to create an application or to rewrap. Set ENCRYPTION_KEYS_RETIRED to
the previous keys during a rotation, then run rewrap.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(command string, args []string) error {
	if command == "keygen" {
		key, err := crypto.NewEncryptionKey()
		if err != nil {
			return err
		}
		fmt.Printf("ENCRYPTION_KEY=%s\n", key)
		return nil
	}
	if command == "hash-password" {
		password, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && len(password) == 0 {
			return fmt.Errorf("read password from stdin: %w", err)
		}
		password = strings.TrimSuffix(strings.TrimSuffix(password, "\n"), "\r")
		hash, err := crypto.HashPassword(password)
		if err != nil {
			return err
		}
		fmt.Println(hash)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Database.URL == "" {
		return errors.New("DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	db, err := postgres.Open(ctx, cfg.Database.URL, postgres.Options{
		MaxOpenConns:    cfg.Database.MaxOpenConns,
		MaxIdleConns:    cfg.Database.MaxIdleConns,
		ConnMaxLifetime: cfg.Database.ConnMaxLifetime,
	})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if command == "migrate" {
		applied, err := postgres.Migrate(ctx, db)
		if err != nil {
			return err
		}
		if len(applied) == 0 {
			fmt.Println("schema is up to date")
			return nil
		}
		for _, version := range applied {
			fmt.Printf("applied %s\n", version)
		}
		return nil
	}

	var cipher *crypto.Cipher
	if cfg.Secrets.EncryptionKey != "" {
		if cipher, err = crypto.NewCipher(cfg.Secrets.EncryptionKey, cfg.Secrets.RetiredKeys...); err != nil {
			return err
		}
	}

	admin := postgres.NewAdmin(postgres.New(db, cipher))

	switch command {
	case "create-application":
		return createApplication(ctx, admin, args)
	case "list-applications":
		return listApplications(ctx, admin)
	case "create-producer":
		return createProducer(ctx, admin, args)
	case "revoke-producer":
		return revokeProducer(ctx, admin, args)
	case "list-producers":
		return listProducers(ctx, admin)
	case "rewrap":
		return rewrap(ctx, admin)
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", command)
	}
}

func createApplication(ctx context.Context, admin *postgres.Admin, args []string) error {
	flags := flag.NewFlagSet("create-application", flag.ExitOnError)
	slug := flags.String("slug", "", "application slug, for example sample")
	name := flags.String("name", "", "display name")
	origin := flags.String("origin", "", "trusted origin, for example https://sample.example")
	subject := flags.String("subject", "", "VAPID contact, a mailto: or https: URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *slug == "" || *name == "" || *origin == "" || *subject == "" {
		return errors.New("slug, name, origin and subject are all required")
	}

	application, publicKey, err := admin.CreateApplication(ctx, *slug, *name, *origin, *subject)
	if err != nil {
		return err
	}

	fmt.Printf("application %s created\n", application.Slug)
	fmt.Printf("id:        %s\n", application.ID)
	fmt.Printf("origin:    %s\n", application.Origin)
	fmt.Printf("vapid key: %s\n", publicKey)
	return nil
}

func createProducer(ctx context.Context, admin *postgres.Admin, args []string) error {
	flags := flag.NewFlagSet("create-producer", flag.ExitOnError)
	application := flags.String("application", "", "application slug")
	name := flags.String("name", "", "producer name, for example sample-api")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *application == "" || *name == "" {
		return errors.New("application and name are both required")
	}

	producer, token, err := admin.CreateProducer(ctx, *application, *name)
	if err != nil {
		return err
	}

	fmt.Printf("producer %s created for %s\n", producer.Name, *application)
	fmt.Printf("id:    %s\n", producer.ID)
	fmt.Printf("token: %s\n", token)
	fmt.Println("This token is shown once and is stored only as a hash.")
	return nil
}

func revokeProducer(ctx context.Context, admin *postgres.Admin, args []string) error {
	flags := flag.NewFlagSet("revoke-producer", flag.ExitOnError)
	application := flags.String("application", "", "application slug")
	name := flags.String("name", "", "producer name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *application == "" || *name == "" {
		return errors.New("application and name are both required")
	}

	if err := admin.RevokeProducer(ctx, *application, *name); err != nil {
		return err
	}

	fmt.Printf("producer %s for %s revoked\n", *name, *application)
	return nil
}

func listApplications(ctx context.Context, admin *postgres.Admin) error {
	applications, err := admin.ListApplications(ctx)
	if err != nil {
		return err
	}
	if len(applications) == 0 {
		fmt.Println("no applications")
		return nil
	}

	for _, application := range applications {
		fmt.Printf("%-16s %-24s %s\n", application.Slug, application.DisplayName, application.Origin)
	}
	return nil
}

func listProducers(ctx context.Context, admin *postgres.Admin) error {
	producers, err := admin.ListProducers(ctx)
	if err != nil {
		return err
	}
	if len(producers) == 0 {
		fmt.Println("no producers")
		return nil
	}

	for _, producer := range producers {
		state := "active"
		if producer.Revoked {
			state = "revoked"
		}
		fmt.Printf("%-16s %-24s %-8s %s\n", producer.Application, producer.Name, state, producer.CreatedAt.Format(time.RFC3339))
	}
	return nil
}

func rewrap(ctx context.Context, admin *postgres.Admin) error {
	rewrapped, err := admin.RewrapVAPIDKeys(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("rewrapped %d application key(s)\n", rewrapped)
	return nil
}
