package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrEmailTaken = errors.New("email already registered")
var ErrUserNotFound = errors.New("user not found")
var ErrInvalidVerificationToken = errors.New("invalid or expired verification token")

// User contains public account information.
// PasswordHash is deliberately excluded so it cannot accidentally
// appear in API responses through JSON serialization.
type User struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// Repository contains database operations for user accounts.
type Repository struct {
	pool *pgxpool.Pool
}

// Credentials is an internal authentication result.
// Never serialize this structure directly into an HTTP response.
type Credentials struct {
	User          User
	PasswordHash  string
	EmailVerified bool
}

// NewRepository creates a user repository using the shared connection pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// CreateCustomer inserts a customer account using parameterized SQL.
// The database generates the UUID and timestamps, while the database
// constraint enforces case-insensitive email uniqueness.
func (r *Repository) CreateCustomer(
	ctx context.Context,
	email string,
	passwordHash string,
) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	const query = `
		INSERT INTO users (email, password_hash, role)
		VALUES ($1, $2, 'customer')
		RETURNING id, email, role, created_at
	`

	var created User

	err := r.pool.QueryRow(
		ctx,
		query,
		email,
		passwordHash,
	).Scan(
		&created.ID,
		&created.Email,
		&created.Role,
		&created.CreatedAt,
	)
	if err != nil {
		// PostgreSQL error 23505 means a unique constraint was violated.
		// Translate the known email uniqueness conflict into a domain error.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) &&
			pgErr.Code == "23505" &&
			pgErr.ConstraintName == "users_email_unique_idx" {
			return User{}, ErrEmailTaken
		}

		return User{}, fmt.Errorf("create customer: %w", err)
	}

	return created, nil
}

// FindCredentialsByEmail retrieves the public user fields and password hash
// needed to authenticate a login attempt.
func (r *Repository) FindCredentialsByEmail(
	ctx context.Context,
	email string,
) (Credentials, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	const query = `
		SELECT id, email, role, created_at, password_hash, email_verified
		FROM users
		WHERE LOWER(email) = $1
	`

	var credentials Credentials

	err := r.pool.QueryRow(ctx, query, email).Scan(
		&credentials.User.ID,
		&credentials.User.Email,
		&credentials.User.Role,
		&credentials.User.CreatedAt,
		&credentials.PasswordHash,
		&credentials.EmailVerified,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Credentials{}, ErrUserNotFound
		}

		return Credentials{}, fmt.Errorf("find user credentials: %w", err)
	}
	return credentials, nil
}

func (r *Repository) FindByID(
	ctx context.Context,
	id uuid.UUID,
) (User, error) {
	const query = `
		SELECT id, email, role, created_at
		FROM users
		WHERE id = $1
	`

	var found User

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&found.ID,
		&found.Email,
		&found.Role,
		&found.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}

		return User{}, fmt.Errorf("find user by ID: %w", err)
	}

	return found, nil
}

// CreateEmailVerificationToken replaces any outstanding unused token
// for the user and stores the new token hash and expiration.
func (r *Repository) CreateEmailVerificationToken(
	ctx context.Context,
	userID uuid.UUID,
	tokenHash string,
	expiresAt time.Time,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin email verification token creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
        DELETE FROM email_verification_tokens
        WHERE user_id = $1 AND used_at IS NULL
    `, userID)
	if err != nil {
		return fmt.Errorf("replace outstanding verification token: %w", err)
	}

	_, err = tx.Exec(ctx, `
        INSERT INTO email_verification_tokens
            (user_id, token_hash, expires_at)
        VALUES ($1, $2, $3)
    `, userID, tokenHash, expiresAt)
	if err != nil {
		return fmt.Errorf("insert email verification token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit email verification token: %w", err)
	}

	return nil
}

// VerifyEmailToken atomically consumes a valid token and verifies its user.
func (r *Repository) VerifyEmailToken(
	ctx context.Context,
	tokenHash string,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin email verification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tokenID uuid.UUID
	var userID uuid.UUID

	err = tx.QueryRow(ctx, `
        SELECT id, user_id
        FROM email_verification_tokens
        WHERE token_hash = $1
          AND used_at IS NULL
          AND expires_at > NOW()
        FOR UPDATE
    `, tokenHash).Scan(&tokenID, &userID)

	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidVerificationToken
	}
	if err != nil {
		return fmt.Errorf("find valid email verification token: %w", err)
	}

	_, err = tx.Exec(ctx, `
        UPDATE email_verification_tokens
        SET used_at = NOW()
        WHERE id = $1
    `, tokenID)
	if err != nil {
		return fmt.Errorf("consume email verification token: %w", err)
	}

	result, err := tx.Exec(ctx, `
        UPDATE users
        SET email_verified = TRUE,
            updated_at = NOW()
        WHERE id = $1
          AND email_verified = FALSE
    `, userID)
	if err != nil {
		return fmt.Errorf("verify user email: %w", err)
	}

	if result.RowsAffected() != 1 {
		return ErrInvalidVerificationToken
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit email verification: %w", err)
	}

	return nil
}

func (r *Repository) FindEmailVerificationStatus(
	ctx context.Context,
	email string,
) (uuid.UUID, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	var id uuid.UUID
	var verified bool

	err := r.pool.QueryRow(ctx, `
        SELECT id, email_verified
        FROM users
        WHERE LOWER(email) = $1
    `, email).Scan(&id, &verified)

	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, ErrUserNotFound
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf(
			"find email verification status: %w", err,
		)
	}

	return id, verified, nil
}

// ListAllAdmin returns a paginated list of users without credentials.
func (r *Repository) ListAllAdmin(
	ctx context.Context,
	limit int,
	offset int,
) ([]User, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := r.pool.Query(ctx, `
        SELECT id, email, role, created_at
        FROM users
        ORDER BY created_at DESC, id DESC
        LIMIT $1 OFFSET $2
    `, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list users for admin: %w", err)
	}
	defer rows.Close()

	users := make([]User, 0)

	for rows.Next() {
		var found User

		if err := rows.Scan(
			&found.ID,
			&found.Email,
			&found.Role,
			&found.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan admin user: %w", err)
		}

		users = append(users, found)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin users: %w", err)
	}

	return users, nil
}

func (r *Repository) PromoteCustomerToOperator(
	ctx context.Context,
	id uuid.UUID,
) (User, error) {
	var updated User

	err := r.pool.QueryRow(ctx, `
		UPDATE users
		SET role = 'operator'
		WHERE id = $1
		  AND role = 'customer'
		RETURNING id, email, role, created_at
	`, id).Scan(
		&updated.ID,
		&updated.Email,
		&updated.Role,
		&updated.CreatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("promote customer to operator: %w", err)
	}

	return updated, nil
}
