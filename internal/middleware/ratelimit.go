package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TrustedProxyHops is the number of reverse proxies in front of the API that
// append to X-Forwarded-For (e.g. 2 for Vercel rewrite -> Render load balancer).
// 0 means requests arrive directly and RemoteAddr is the client.
var TrustedProxyHops = 0

// ClientIP returns the caller's IP, taking trusted proxies into account.
func ClientIP(r *http.Request) string {
	if TrustedProxyHops > 0 {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			i := len(parts) - TrustedProxyHops
			if i < 0 {
				i = 0
			}
			if ip := strings.TrimSpace(parts[i]); ip != "" {
				return ip
			}
		}
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// RateLimit allows at most `limit` requests per client IP in each `window`.
// It is a simple in-memory fixed window, enough to stop form spam on a
// single server. Behind reverse proxies, set TrustedProxyHops so the client
// IP is read from X-Forwarded-For.
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
		ip := ClientIP(r)
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
