package middleware

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// TracingMiddleware creates a middleware that starts a server span per request.
// Spans are named after the request method and route pattern.
func TracingMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, "http.server",
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
				route := r.Pattern
				if route == "" {
					route = r.URL.Path
				}
				return r.Method + " " + route
			}),
		)
	}
}
