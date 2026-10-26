package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNewTokenManagerRejectsShortSecret(t *testing.T) {
	_, err := NewTokenManager("too-short")
	require.Error(t, err)
}

func TestNewTokenManagerAcceptsValidSecret(t *testing.T) {
	manager, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)
	require.NotNil(t, manager)
}

func TestCreateAndValidateAccessToken(t *testing.T) {
	manager, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)

	userID := uuid.New()
	rawToken, err := manager.CreateAccessToken(userID, "customer")
	require.NoError(t, err)

	claims, err := manager.ValidateAccessToken(rawToken)
	require.NoError(t, err)
	require.Equal(t, userID.String(), claims.Subject)
	require.Equal(t, "customer", claims.Role)
	require.Equal(t, tokenIssuer, claims.Issuer)
	require.NotEmpty(t, claims.ID)
	require.NotNil(t, claims.ExpiresAt)
}

func TestCreateAccessTokenRejectsInvalidRole(t *testing.T) {
	manager, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)

	_, err = manager.CreateAccessToken(uuid.New(), "superadmin")
	require.Error(t, err)
}

func TestValidateAccessTokenRejectsExpiredToken(t *testing.T) {
	manager, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)

	now := time.Now()
	claims := AccessClaims{
		Role: "customer",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   uuid.NewString(),
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now.Add(-time.Hour)),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Hour)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-time.Minute)),
		},
	}

	rawToken, err := jwt.NewWithClaims(
		jwt.SigningMethodHS256,
		claims,
	).SignedString(manager.secret)
	require.NoError(t, err)

	_, err = manager.ValidateAccessToken(rawToken)
	require.Error(t, err)
}

func TestValidateAccessTokenRejectsWrongIssuer(t *testing.T) {
	manager, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)

	now := time.Now()
	claims := AccessClaims{
		Role: "customer",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "not-trailnest",
			Subject:   uuid.NewString(),
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	}

	rawToken, err := jwt.NewWithClaims(
		jwt.SigningMethodHS256,
		claims,
	).SignedString(manager.secret)
	require.NoError(t, err)

	_, err = manager.ValidateAccessToken(rawToken)
	require.Error(t, err)
}

func TestValidateAccessTokenRejectsWrongSigningSecret(t *testing.T) {
	issuer, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)

	validator, err := NewTokenManager(
		"another-valid-secret-that-is-at-least-32-bytes",
	)
	require.NoError(t, err)

	rawToken, err := issuer.CreateAccessToken(uuid.New(), "customer")
	require.NoError(t, err)

	_, err = validator.ValidateAccessToken(rawToken)
	require.Error(t, err)
}

func TestValidateAccessTokenRejectsMalformedToken(t *testing.T) {
	manager, err := NewTokenManager(testJWTSecret)
	require.NoError(t, err)

	_, err = manager.ValidateAccessToken("not.a.valid.jwt")
	require.Error(t, err)
}
