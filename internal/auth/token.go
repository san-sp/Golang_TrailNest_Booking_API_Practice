package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const tokenIssuer = "trailnest"

const accessTokenLifetime = 15 * time.Minute

// AccessClaims contains the identity and permissions carried by an access token.
type AccessClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// TokenManager handles signed access tokens.
type TokenManager struct {
	secret []byte
}

// NewTokenManager creates a token manager using a secret supplied by configuration.
func NewTokenManager(secret string) (*TokenManager, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must contain at least 32 bytes")
	}

	return &TokenManager{
		secret: []byte(secret),
	}, nil
}

// CreateAccessToken issues a short-lived access token for an existing user.
func (m *TokenManager) CreateAccessToken(
	userID uuid.UUID,
	role string,
) (string, error) {
	if role != "customer" && role != "operator" && role != "admin" {
		return "", fmt.Errorf("invalid user role")
	}

	now := time.Now()

	claims := AccessClaims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   userID.String(),
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTokenLifetime)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString(m.secret)
}

// ValidateAccessToken verifies the signature and registered claims.
// The returned claims must only be used after validation succeeds.
func (m *TokenManager) ValidateAccessToken(
	rawToken string,
) (*AccessClaims, error) {
	claims := &AccessClaims{}

	token, err := jwt.ParseWithClaims(
		rawToken,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing algorithm")
			}

			return m.secret, nil
		},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		return nil, fmt.Errorf("validate access token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid access token")
	}

	if _, err := uuid.Parse(claims.Subject); err != nil {
		return nil, fmt.Errorf("invalid token subject")
	}

	if claims.Role != "customer" &&
		claims.Role != "operator" &&
		claims.Role != "admin" {
		return nil, fmt.Errorf("invalid token role")
	}

	return claims, nil
}
