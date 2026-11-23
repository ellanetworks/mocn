package api

import (
	"context"
	"time"

	"github.com/ellanetworks/mocn/internal/db"
)

type userStore interface {
	CountUsers(ctx context.Context) (int, error)
	CreateUser(ctx context.Context, u db.NewUser, maxUsers int) (int64, error)
	GetUser(ctx context.Context, email string) (db.User, error)
	GetUserByID(ctx context.Context, id int64) (db.User, error)
	ListUsers(ctx context.Context, page, perPage int) ([]db.User, int, error)
	UpdateUserRole(ctx context.Context, id int64, role db.Role) error
	UpdateUserPassword(ctx context.Context, id int64, passwordHash string, keepSession []byte) error
	DeleteUser(ctx context.Context, id int64) error
}

type oidcStore interface {
	DeleteIdentities(ctx context.Context, userID int64) error
	LogInWithOIDC(ctx context.Context, l db.OIDCLogin) (db.OIDCLoginResult, error)
	LinkOIDCIdentity(ctx context.Context, l db.OIDCLink) error
	UseOIDCState(ctx context.Context, stateHash []byte, expiresAt, now time.Time) error
}

type sessionStore interface {
	CreateSession(ctx context.Context, s db.NewSession, maxPerUser int) (int64, error)
	GetSession(ctx context.Context, id int64) (db.Session, error)
	GetSessionByTokenHash(ctx context.Context, tokenHash []byte) (db.Session, error)
	DeleteSessionByTokenHash(ctx context.Context, tokenHash []byte) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
}

type apiTokenStore interface {
	CreateAPIToken(ctx context.Context, t db.NewAPIToken) (int64, error)
	GetAPITokenByTokenID(ctx context.Context, tokenID string) (db.APIToken, error)
	ListAPITokens(ctx context.Context, userID int64, page, perPage int) ([]db.APIToken, int, error)
	CountAPITokens(ctx context.Context, userID int64) (int, error)
	DeleteAPIToken(ctx context.Context, userID int64, tokenID string) error
	TouchAPIToken(ctx context.Context, id int64, at time.Time) error
}

type authStore interface {
	userStore
	oidcStore
	sessionStore
	apiTokenStore
}
