package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	slogctx "github.com/veqryn/slog-context"
)

type TemperatureReadingPayload struct {
	TempCo    float64 `json:"tempCo"`
	TempRoom  float64 `json:"tempRoom"`
	Timestamp *int64  `json:"timestamp"`
}

type TemperatureReading struct {
	Id        int     `json:"id"`
	TempCo    float64 `json:"tempCo"`
	TempRoom  float64 `json:"tempRoom"`
	Timestamp *int64  `json:"timestamp"`
}

type app struct {
	db *pgxpool.Pool
}

func main() {
	h := slogctx.NewHandler(slog.NewJSONHandler(os.Stdout, nil), nil)
	logger := slog.New(h)
	slog.SetDefault(logger)

	host := flag.String("host", "127.0.0.1", "Server host")
	port := flag.Int("port", 8080, "Server port")
	dbHost := flag.String("db-host", "localhost", "Database host")
	dbPort := flag.Int("db-port", 5432, "Database port")
	dbUser := flag.String("db-user", "user", "Database user")
	dbPass := flag.String("db-pass", "", "Database password")
	dbName := flag.String("db-name", "dbname", "Database name")
	flag.Parse()

	// env variables take precedence, prefix APP_
	if env := os.Getenv("APP_HOST"); env != "" {
		*host = env
		logger.Debug("flag host overridden by env APP_HOST", "value", env)
	}
	if env := os.Getenv("APP_PORT"); env != "" {
		if p, err := strconv.Atoi(env); err == nil {
			*port = p
			logger.Debug("flag port overridden by env APP_PORT", "value", p)
		}
	}
	if env := os.Getenv("APP_DB_HOST"); env != "" {
		*dbHost = env
		logger.Debug("flag db-host overridden by env APP_DB_HOST", "value", env)
	}
	if env := os.Getenv("APP_DB_PORT"); env != "" {
		if p, err := strconv.Atoi(env); err == nil {
			*dbPort = p
			logger.Debug("flag db-port overridden by env APP_DB_PORT", "value", p)
		}
	}
	if env := os.Getenv("APP_DB_USER"); env != "" {
		*dbUser = env
		logger.Debug("flag db-user overridden by env APP_DB_USER", "value", env)
	}
	if env := os.Getenv("APP_DB_PASS"); env != "" {
		*dbPass = env
		logger.Debug("flag db-pass overridden by env APP_DB_PASS", "value", "***")
	}
	if env := os.Getenv("APP_DB_NAME"); env != "" {
		*dbName = env
		logger.Debug("flag db-name overridden by env APP_DB_NAME", "value", env)
	}

	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?application_name=esp8266-web",
		*dbUser, *dbPass, *dbHost, *dbPort, *dbName)
	ctx := context.Background()
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		logger.Error("Failed to parse database config", "error", err)
		os.Exit(1)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		logger.Error("Failed to create database pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logger.Error("Failed to ping database", "error", err)
		os.Exit(1)
	}

	app := &app{db: pool}

	if err := app.applyMigrations(ctx); err != nil {
		logger.Error("Failed to apply migrations", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/health", panicRecoveryMiddleware(logger)(requestIdMiddleware(logger)(loggingMiddleware(http.HandlerFunc(app.healthHandler)))))

	mux.Handle("/", panicRecoveryMiddleware(logger)(requestIdMiddleware(logger)(loggingMiddleware(http.HandlerFunc(app.homeHandler)))))
	mux.Handle("/data", panicRecoveryMiddleware(logger)(requestIdMiddleware(logger)(loggingMiddleware(http.HandlerFunc(app.dataHandler)))))

	addr := fmt.Sprintf("%s:%d", *host, *port)
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	logger.Info(fmt.Sprintf("starting server at http://%s", addr), slog.String("addr", addr))
	if err := server.ListenAndServe(); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func (a *app) healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"status": "ok"}`)
}

func (a *app) homeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := os.ReadFile("index.html")
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write(data)
}

func (a *app) dataHandler(w http.ResponseWriter, r *http.Request) {
	logger := slogctx.FromCtx(r.Context())

	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodPost:
		var tri TemperatureReadingPayload
		if err := json.NewDecoder(r.Body).Decode(&tri); err != nil {
			logger.Error("failed to decode temperature reading",
				slog.Any("error", err),
			)
			http.Error(w, "Bad request", http.StatusUnprocessableEntity)
			return
		}
		logger.Info("Received temperature reading",
			slog.Any("data", tri),
		)
		if tri.Timestamp == nil {
			now := time.Now().UTC().Unix()
			tri.Timestamp = &now
		}
		var tr TemperatureReading
		err := a.db.QueryRow(r.Context(), `
			INSERT INTO readings (temp_co, temp_room, timestamp)
			VALUES ($1, $2, $3)
			RETURNING id, temp_co, temp_room, timestamp
		`, tri.TempCo, tri.TempRoom, *tri.Timestamp).Scan(&tr.Id, &tr.TempCo, &tr.TempRoom, &tr.Timestamp)
		if err != nil {
			logger.Error("Failed to insert temperature reading", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(tr)

	case http.MethodGet:
		limitStr := r.URL.Query().Get("limit")
		limit := 10
		if limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
				limit = l
			}
		}

		offsetStr := r.URL.Query().Get("offset")
		offset := 0
		if offsetStr != "" {
			if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
				offset = o
			}
		}

		rows, err := a.db.Query(r.Context(), `
			SELECT id, temp_co, temp_room, timestamp
			FROM readings
			ORDER BY timestamp DESC
			LIMIT $1 OFFSET $2
		`, limit, offset)
		if err != nil {
			logger.Error("Failed to query temperature readings", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		readings := make([]TemperatureReading, 0)
		for rows.Next() {
			var tr TemperatureReading
			if err := rows.Scan(&tr.Id, &tr.TempCo, &tr.TempRoom, &tr.Timestamp); err != nil {
				logger.Error("Failed to scan row", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			readings = append(readings, tr)
		}
		if err := rows.Err(); err != nil {
			logger.Error("Rows error", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(readings)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}

}

func (a *app) applyMigrations(ctx context.Context) error {
	slog.Debug("Applying migrations")
	_, err := a.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS readings (
			id SERIAL PRIMARY KEY,
			temp_co DOUBLE PRECISION,
			temp_room DOUBLE PRECISION,
			timestamp BIGINT,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`)
	if err != nil {
		return err
	}
	slog.Debug("Migrations applied successfully")
	return nil
}

func requestIdMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := slogctx.NewCtx(r.Context(), logger)

			requestID := uuid.New().String()
			w.Header().Set("X-Request-ID", requestID)
			ctx = slogctx.With(ctx, slog.String("request_id", requestID))

			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func panicRecoveryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					reqLogger := slogctx.FromCtx(r.Context())
					if reqLogger == nil {
						reqLogger = logger
					}
					reqLogger.Error("panic recovered", slog.Any("panic", err))
					http.Error(w, "Internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		reqLogger := slogctx.FromCtx(r.Context())
		reqLogger.Info("request",
			slog.String("method", r.Method),
			slog.String("url", r.URL.Path),
			slog.String("remote", r.RemoteAddr),
			slog.String("user_agent", r.UserAgent()),
		)

		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		duration := time.Since(start)

		reqLogger.Info("response",
			slog.String("method", r.Method),
			slog.String("url", r.URL.Path),
			slog.Int("status", rw.statusCode),
			slog.Duration("duration", duration),
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
