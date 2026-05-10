package httpserver

import (
	"net/http"
	"time"

	"github.com/yereke99/stone/internal/http/handler"
	"go.uber.org/zap"
)

func NewRouter(health *handler.HealthHandler, webhook *handler.WebhookHandler, logger *zap.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /health", health)
	mux.Handle("GET /webhook", webhook)
	mux.Handle("POST /webhook", webhook)

	return recoverMiddleware(logger, accessLogMiddleware(logger, mux))
}

func accessLogMiddleware(logger *zap.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(recorder, r)

		logger.Info("http request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", recorder.statusCode),
			zap.Duration("duration", time.Since(startedAt)),
		)
	})
}

func recoverMiddleware(logger *zap.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("http panic recovered", zap.Any("panic", recovered))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}
