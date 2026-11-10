package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jmoiron/sqlx"
)

func NewRouter() http.Handler {
	return newRouter(nil)
}

type imageUploader interface {
	Put(ctx context.Context, key, contentType string, data []byte) error
}

func newRouter(registration *registrationService, uploaders ...imageUploader) http.Handler {
	return newRouterWithFrontend(registration, nil, uploaders...)
}

func newRouterWithFrontend(registration *registrationService, frontend fs.FS, uploaders ...imageUploader) http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.Recoverer)
	router.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	})
	if registration != nil {
		router.Get("/api/captcha", registration.captcha)
		router.Post("/api/guest", registration.issueGuest)
		router.Post("/api/register", registration.register)
		router.Post("/api/login", registration.login)
		router.Get("/api/user", registration.getUser)
		router.Delete("/api/user", registration.deleteUser)
		if len(uploaders) > 0 && uploaders[0] != nil {
			router.Post("/api/images", registration.uploadImage(uploaders[0]))
		}
	}
	if frontend != nil {
		router.NotFound(frontendHandler(frontend).ServeHTTP)
	}
	return router
}

func Serve(ctx context.Context, addr string, frontend fs.FS) error {
	return serve(ctx, addr, newRouterWithFrontend(nil, frontend))
}

func ServeWithRegistration(ctx context.Context, addr string, db *sqlx.DB, model faceModel, encryptionKey, jwtKey []byte, threshold float64, uploader imageUploader, frontend fs.FS) error {
	registration, err := newRegistrationService(db, model, encryptionKey, jwtKey, threshold)
	if err != nil {
		return fmt.Errorf("configure registration: %w", err)
	}
	return serve(ctx, addr, newRouterWithFrontend(registration, frontend, uploader))
}

func serve(ctx context.Context, addr string, handler http.Handler) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
	}
	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(listener)
	}()
	log.Printf("API server listening on %s", listener.Addr())

	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			closeErr := srv.Close()
			<-done
			return errors.Join(fmt.Errorf("shutdown HTTP server: %w", err), closeErr)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}
}
