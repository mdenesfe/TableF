// Command tablef-web serves TableF in the browser.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mdenesfe/TableF/internal/core"
	_ "github.com/mdenesfe/TableF/internal/engines"
	"github.com/mdenesfe/TableF/internal/ui"
	"github.com/mdenesfe/TableF/internal/web"
)

func main() {
	addr := flag.String("addr", envOr("TABLEF_ADDR", "127.0.0.1:7070"), "listen address")
	token := flag.String("token", os.Getenv("TABLEF_TOKEN"), "require this bearer token for API calls")
	storePath := flag.String("store", os.Getenv("TABLEF_STORE"), "path of the saved connections file")
	flag.Parse()

	if *storePath == "" {
		p, err := core.DefaultStorePath()
		if err != nil {
			log.Fatal(err)
		}
		*storePath = p
	}

	if host, _, err := net.SplitHostPort(*addr); err == nil && !isLoopback(host) && *token == "" {
		log.Printf("WARNING: listening on %s without -token; only requests addressed to localhost are served", *addr)
	}

	svc := core.NewService(core.NewStore(*storePath))
	defer svc.Shutdown()

	handler := web.NewHandler(svc, ui.Assets(), *token).
		// Calls stop when the browser abandons the request.
		BindContext(func(ctx context.Context) any { return core.WithContext(svc, ctx) })
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: a long query legitimately takes minutes to answer.
	}

	go func() {
		log.Printf("TableF web running at http://%s", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
