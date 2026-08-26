package webapi

import "net/http"

// SecurityHeaders returns middleware that sets defensive response headers on
// every web UI/API response. The CSP allows the SvelteKit SPA bundle plus the
// inline styles Flowbite emits, and blocks framing, MIME sniffing, and
// referrer leakage. scriptHashes carries base64 SHA-256 hashes of the SPA's
// inline bootstrap scripts (see webui.CSPScriptHashes); without them a strict
// script-src would block SvelteKit hydration.
func SecurityHeaders(scriptHashes []string, next http.Handler) http.Handler {
	csp := BuildCSP(scriptHashes)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// BuildCSP constructs the Content-Security-Policy for the web UI. Inline
// scripts are allowed only via the provided hashes (never 'unsafe-inline').
func BuildCSP(scriptHashes []string) string {
	scriptSrc := "script-src 'self'"
	for _, hash := range scriptHashes {
		if hash == "" {
			continue
		}
		scriptSrc += " 'sha256-" + hash + "'"
	}
	return "default-src 'self'; " +
		scriptSrc + "; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; " +
		"font-src 'self' data:; " +
		"connect-src 'self'; " +
		"object-src 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'"
}
