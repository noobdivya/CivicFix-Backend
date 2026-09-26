package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimit allows at most `limit` requests per client IP in each `window`.
// It is a simple in-memory fixed window, enough to stop form spam on a
// single server. (Behind a reverse proxy, the client IP would need to come
// from X-Forwarded-For instead of RemoteAddr.)
func RateLimit(limit int, window time.Duration, next http.HandlerFunc) http.HandlerFunc {
	type bucket struct {
		count int
		reset time.Time
	}
	var (
		mu      sync.Mutex
		buckets = map[string]*bucket{}
		lastGC  = time.Now()
	)

	return func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		now := time.Now()

		mu.Lock()
		if now.Sub(lastGC) > window {
			for k, b := range buckets {
				if now.After(b.reset) {
					delete(buckets, k)
				}
			}
			lastGC = now
		}
		b, ok := buckets[ip]
		if !ok || now.After(b.reset) {
			b = &bucket{reset: now.Add(window)}
			buckets[ip] = b
		}
		b.count++
		allowed := b.count <= limit
		mu.Unlock()

		if !allowed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"too many submissions, please try again in a few minutes"}`))
			return
		}
		next(w, r)
	}
}
