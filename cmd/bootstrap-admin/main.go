package main

import (
	"bufio"
	"context"
	"fmt"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
	"golang.org/x/term"

	"trailnest/internal/auth"
	"trailnest/internal/config"
	"trailnest/internal/database"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Bootstrap admin failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	_ = godotenv.Load()

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Admin email: ")
	emailInput, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read email: %w", err)
	}

	email := strings.ToLower(strings.TrimSpace(emailInput))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return fmt.Errorf("enter a valid email address")
	}

	fmt.Print("Admin password (input hidden): ")
	passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	password := string(passwordBytes)

	if len(password) < 12 {
		return fmt.Errorf("password must contain at least 12 characters")
	}

	fmt.Print("Confirm password (input hidden): ")
	confirmationBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("read password confirmation: %w", err)
	}

	if password != string(confirmationBytes) {
		return fmt.Errorf("passwords do not match")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	// Serialize bootstrap attempts so two commands cannot create
	// the first admin concurrently.
	_, err = tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('trailnest.bootstrap_admin'))`)
	if err != nil {
		return fmt.Errorf("lock admin bootstrap: %w", err)
	}

	var adminExists bool
	err = tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE role = 'admin')`,
	).Scan(&adminExists)
	if err != nil {
		return fmt.Errorf("check existing admin: %w", err)
	}

	if adminExists {
		return fmt.Errorf(
			"an admin already exists; bootstrap is only for the initial admin",
		)
	}

	var id, createdAt string
	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, email_verified)
		VALUES ($1, $2, 'admin', TRUE)
		RETURNING id::text, created_at::text
	`, email, passwordHash).Scan(&id, &createdAt)
	if err != nil {
		return fmt.Errorf("create admin: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit admin creation: %w", err)
	}

	fmt.Println("\nInitial admin created successfully!")
	fmt.Printf("Email: %s\n", email)
	fmt.Printf("User ID: %s\n", id)
	fmt.Println("You can now log in using the existing login endpoint.")

	return nil
}
