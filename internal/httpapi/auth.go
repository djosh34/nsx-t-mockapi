// Package httpapi owns HTTP request handling boundaries for the mock API.
package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"

	appsqlite "nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

const basicAuthChallenge = `Basic realm="nsx-t-mockapi"`

type authenticatedUserContextKey struct{}

// AuthenticatedUser is the local user identity accepted at the HTTP boundary.
type AuthenticatedUser struct {
	Username string
	Role     appsqlite.Role
}

type userFinder interface {
	FindUser(ctx context.Context, username string) (appsqlite.User, bool, error)
}

// UserFromContext returns the authenticated user attached by RequireBasicAuth.
func UserFromContext(ctx context.Context) (AuthenticatedUser, bool) {
	user, ok := ctx.Value(authenticatedUserContextKey{}).(AuthenticatedUser)
	return user, ok
}

// RequireBasicAuth requires HTTP Basic credentials before serving next.
func RequireBasicAuth(logger *zap.Logger, users userFinder, next http.Handler) http.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		logger.Debug("parsed basic auth header", zap.Bool("present", ok), zap.String("username", username))
		if !ok {
			writeUnauthorized(w)
			return
		}

		user, found, err := users.FindUser(r.Context(), username)
		if err != nil {
			logger.Error("basic auth user lookup failed", zap.String("username", username), zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if !found {
			logger.Debug("basic auth user not found", zap.String("username", username))
			writeUnauthorized(w)
			return
		}
		if subtle.ConstantTimeCompare([]byte(user.Password), []byte(password)) != 1 {
			logger.Debug("basic auth password rejected", zap.String("username", username))
			writeUnauthorized(w)
			return
		}
		logger.Debug("basic auth user accepted", zap.String("username", user.Username), zap.String("role", string(user.Role)))

		authenticated := AuthenticatedUser{Username: user.Username, Role: user.Role}
		ctx := context.WithValue(r.Context(), authenticatedUserContextKey{}, authenticated)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", basicAuthChallenge)
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}
