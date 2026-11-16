package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
)

// Serve answers on ln until ctx is cancelled, then stops accepting
// connections and gives in-flight requests ShutdownTimeout to finish.
func Serve(ctx context.Context, ln net.Listener, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: ReadHeaderTimeout,
		MaxHeaderBytes:    MaxHeaderBytes,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close()
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
