// Command api is the Text-to-Spaceship public API: it starts design runs on the
// agent service, persists what they report, and streams progress to browsers.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/agentclient"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/designs"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/events"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/httpapi"
	"github.com/adrianapsay/meng-spaceship-prototyping/api/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, env("DATABASE_URL", "postgres://spaceship:spaceship@localhost:5432/spaceship"))
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := store.Migrate(ctx, pool); err != nil {
		return err
	}
	queries := store.New(pool)
	if n, err := queries.FailInterruptedDesigns(ctx); err != nil {
		return err
	} else if n > 0 {
		log.Warn("marked interrupted designs as failed", "count", n)
	}

	hub := events.NewHub()
	svc := designs.NewService(queries, hub, agentclient.New(env("AGENT_URL", "http://localhost:8001")), log)

	srv := &http.Server{
		Addr: ":" + env("PORT", "8080"),
		Handler: (&httpapi.Server{
			Queries:       queries,
			Designs:       svc,
			Hub:           hub,
			ArtifactsRoot: env("ARTIFACTS_ROOT", "../cad/artifacts"),
			CORSOrigin:    env("CORS_ORIGIN", "http://localhost:5173"),
			Ping:          pool.Ping,
			Log:           log,
		}).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	svc.Close() // cancel running designs and record their outcome first
	return srv.Shutdown(shutdownCtx)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
