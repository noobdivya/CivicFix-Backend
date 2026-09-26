// Package auth handles staff login sessions and role-based access.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	CookieName = "cf_session"
	sessionTTL = 7 * 24 * time.Hour

	RoleAdmin      = "admin"
	RoleDepartment = "department"
	RoleWorker     = "worker"
)

// User is the logged-in staff member attached to a request.
type User struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Email          string  `json:"email"`
	Phone          string  `json:"phone"`
	Role           string  `json:"role"`
	DepartmentID   *int    `json:"departmentId"`
	DepartmentName *string `json:"departmentName"`
}

type ctxKey struct{}

// FromContext returns the logged-in user, or nil for anonymous requests.
func FromContext(ctx context.Context) *User {
	u, _ := ctx.Value(ctxKey{}).(*User)
	return u
}

type Service struct {
	DB           *pgxpool.Pool
	SecureCookie bool // set true when served over HTTPS
}

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(b), err
}

// dummyHash is compared against when the email doesn't exist, so a failed
// login takes the same time whether or not the account exists.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("civicfix-dummy-password"), 12)

// Authenticate checks an email/password pair and returns the active user.
func (s *Service) Authenticate(ctx context.Context, email, password string) (*User, error) {
	var (
		u    User
		hash string
	)
	err := s.DB.QueryRow(ctx, `
		SELECT u.id, u.name, u.email, u.phone, u.role, u.department_id, d.name, u.password_hash
		FROM users u LEFT JOIN departments d ON d.id = u.department_id
		WHERE lower(u.email) = lower($1) AND u.active`, strings.TrimSpace(email),
	).Scan(&u.ID, &u.Name, &u.Email, &u.Phone, &u.Role, &u.DepartmentID, &u.DepartmentName, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, nil
	}
	_, _ = s.DB.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, u.ID)
	return &u, nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// StartSession creates a session and sets the HttpOnly session cookie.
func (s *Service) StartSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64) error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	token := hex.EncodeToString(b)
	expires := time.Now().Add(sessionTTL)
	ua := r.UserAgent()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	if _, err := s.DB.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, user_agent, expires_at) VALUES ($1, $2, $3, $4)`,
		tokenHash(token), userID, ua, expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.SecureCookie,
	})
	// Opportunistic cleanup of expired sessions.
	_, _ = s.DB.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return nil
}

// EndSession deletes the current session and clears the cookie.
func (s *Service) EndSession(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		_, _ = s.DB.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.SecureCookie,
	})
}

// Middleware attaches the logged-in user (if any) to the request context.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		var u User
		err = s.DB.QueryRow(r.Context(), `
			SELECT u.id, u.name, u.email, u.phone, u.role, u.department_id, d.name
			FROM sessions s
			JOIN users u ON u.id = s.user_id
			LEFT JOIN departments d ON d.id = u.department_id
			WHERE s.token_hash = $1 AND s.expires_at > now() AND u.active`, tokenHash(c.Value),
		).Scan(&u.ID, &u.Name, &u.Email, &u.Phone, &u.Role, &u.DepartmentID, &u.DepartmentName)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				log.Printf("session lookup: %v", err)
			}
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, &u)))
	})
}

// Require only lets through logged-in users with one of the given roles
// (any role if none are given).
func Require(roles ...string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			u := FromContext(r.Context())
			if u == nil {
				writeErr(w, http.StatusUnauthorized, "please log in")
				return
			}
			if len(roles) > 0 && !slices.Contains(roles, u.Role) {
				writeErr(w, http.StatusForbidden, "you don't have access to this")
				return
			}
			next(w, r)
		}
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// EnsureAdmin creates the first admin account if no admin exists yet.
// If password is empty a random one is generated and printed once.
func (s *Service) EnsureAdmin(ctx context.Context, email, password string) error {
	var exists bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE role = 'admin')`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	generated := password == ""
	if generated {
		b := make([]byte, 9)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		password = hex.EncodeToString(b)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := s.DB.Exec(ctx,
		`INSERT INTO users (name, email, password_hash, role) VALUES ('Administrator', $1, $2, 'admin')`,
		email, hash); err != nil {
		return err
	}
	if generated {
		log.Printf("created admin account %s with password %s (set ADMIN_PASSWORD in .env to choose your own)", email, password)
	} else {
		log.Printf("created admin account %s", email)
	}
	return nil
}
