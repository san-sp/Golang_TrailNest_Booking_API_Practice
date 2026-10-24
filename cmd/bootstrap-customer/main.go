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
		fmt.Fprintf(os.Stderr, "Customer bootstrap failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	_ = godotenv.Load()

	if os.Getenv("TRAILNEST_ALLOW_DEV_CUSTOMER_BOOTSTRAP") != "true" {
		return fmt.Errorf(
			"set TRAILNEST_ALLOW_DEV_CUSTOMER_BOOTSTRAP=true to enable this development command",
		)
	}

	if strings.EqualFold(strings.TrimSpace(os.Getenv("GIN_MODE")), "release") {
		return fmt.Errorf("customer bootstrap is disabled in release mode")
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Customer email: ")
	input, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read email: %w", err)
	}

	email := strings.ToLower(strings.TrimSpace(input))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return fmt.Errorf("enter a valid email address")
	}

	fmt.Print("Customer password (input hidden): ")
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
	confirmation, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("read password confirmation: %w", err)
	}
	if password != string(confirmation) {
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

	var id string
	err = pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, email_verified)
		VALUES ($1, $2, 'customer', TRUE)
		RETURNING id::text
	`, email, passwordHash).Scan(&id)

	if err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("customer was not created")
		}
		return fmt.Errorf("create customer: %w", err)
	}

	fmt.Println("\nDevelopment customer created successfully!")
	fmt.Printf("Email: %s\n", email)
	fmt.Printf("User ID: %s\n", id)
	fmt.Println("Email marked verified for local development.")

	return nil
}
