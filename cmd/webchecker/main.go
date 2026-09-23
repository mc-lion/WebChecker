package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"webchecker/internal/alerter"
	"webchecker/internal/checker"
	"webchecker/internal/config"
	"webchecker/internal/db"
	"webchecker/internal/scheduler"
	"webchecker/internal/store"
	"webchecker/internal/telegram"
	"webchecker/internal/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	database, err := db.Connect(ctx, cfg)
	if err != nil {
		slog.Error("mysql", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := db.Migrate(ctx, database); err != nil {
		slog.Error("migrate", "error", err)
		os.Exit(1)
	}

	st := store.New(database)
	tg := telegram.New(cfg)
	al := alerter.New(st, tg, cfg.TelegramSlowAlerts)
	chk := checker.NewWithOptions(cfg.BlockPrivateHosts)
	sched := scheduler.New(st, chk, al, cfg.CheckerWorkers, cfg.StatsRetentionDays)

	slog.Info("telegram", "configured", tg.Configured(), "alerts", tg.Enabled(), "slow_alerts", cfg.TelegramSlowAlerts)
	if tg.Configured() && !tg.Enabled() {
		slog.Warn("TELEGRAM_ENABLED=false: тестовые сообщения работают, алерты о недоступности и медленном ответе выключены")
	}

	var background sync.WaitGroup
	background.Add(1)
	go func() {
		defer background.Done()
		sched.Run(ctx)
	}()
	if tg.Configured() {
		background.Add(1)
		go func() {
			defer background.Done()
			telegram.RunBot(ctx, tg, st)
		}()
	}

	srv, err := web.New(cfg, st, tg, sched)
	if err != nil {
		slog.Error("web", "error", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		// Импорт дампа может занять минуты, а экспорт — отдавать сотни
		// мегабайт, поэтому таймауты щедрые: они нужны против зависших
		// соединений, а не для ограничения нормальной работы.
		ReadTimeout:  30 * time.Minute,
		WriteTimeout: 30 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}

	go func() {
		slog.Info("http server started", "addr", cfg.HTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)

	// Даём планировщику и боту закончить: иначе процесс уходит, пока воркеры
	// ещё дописывают результаты проверок.
	done := make(chan struct{})
	go func() {
		background.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		slog.Warn("background workers did not stop in time")
	}
	slog.Info("stopped")
}
