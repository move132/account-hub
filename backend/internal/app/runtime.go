package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Start starts background processing explicitly. Constructing an Application
// does not execute queued work. Repeated calls to Start are harmless.
func (a *Application) Start(ctx context.Context) error {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.stopping {
		return errors.New("application is shutting down")
	}
	if a.started {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	workerContext, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.started = true
	interval := a.config.JobScanInterval
	if interval <= 0 {
		interval = JobScanInterval
	}
	a.workers.Add(3)
	go a.runWorker(workerContext, "jobs", func(ctx context.Context) error {
		return a.jobs.Start(ctx, interval, a.config.RefreshWorkerConcurrency)
	})
	go a.runWorker(workerContext, "scheduler", func(ctx context.Context) error {
		a.scheduler.Start(ctx, interval)
		return nil
	})
	go a.runWorker(workerContext, "maintenance", a.maintain)
	return nil
}

func (a *Application) runWorker(ctx context.Context, name string, run func(context.Context) error) {
	defer a.workers.Done()
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		err = fmt.Errorf("%s worker: %w", name, err)
		a.lifecycleMu.Lock()
		a.backgroundErr = errors.Join(a.backgroundErr, err)
		cancel := a.cancel
		a.lifecycleMu.Unlock()
		a.logger.Error("background worker stopped", "worker", name, "error", err)
		cancel()
		select {
		case a.runErrors <- err:
		default:
		}
	}
}

func (a *Application) maintain(ctx context.Context) error {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if a.maintenance != nil {
			if err := a.maintenance(ctx); err != nil && ctx.Err() == nil {
				a.logger.Error("expired session cleanup failed", "error", err)
			}
		}
		stats := a.db.Stats()
		a.logger.Info("database pool", "open_connections", stats.OpenConnections,
			"in_use", stats.InUse, "wait_count", stats.WaitCount,
			"wait_duration_ms", stats.WaitDuration.Milliseconds())
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (a *Application) ready(ctx context.Context) error {
	a.lifecycleMu.Lock()
	started, stopping, workerErr := a.started, a.stopping, a.backgroundErr
	a.lifecycleMu.Unlock()
	if !started || stopping || !a.jobs.Ready() {
		return errors.New("application is not running")
	}
	if workerErr != nil {
		return workerErr
	}
	return a.db.PingContext(ctx)
}

// Run owns the HTTP serving goroutine and reports background failures to the
// process owner, which must always call Shutdown before exiting.
func (a *Application) Run(listener net.Listener) error {
	if err := a.Start(context.Background()); err != nil {
		return err
	}
	a.logger.Info("account-hub starting", "address", a.config.ListenAddress)
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- a.server.Serve(listener) }()
	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-a.runErrors:
		return err
	}
}

// Shutdown stops task intake and active work, drains HTTP requests, and closes
// storage only after workers have finished their bounded result commits.
func (a *Application) Shutdown(ctx context.Context) error {
	a.shutdownOnce.Do(func() {
		a.lifecycleMu.Lock()
		a.stopping = true
		if a.cancel != nil {
			a.cancel()
		}
		a.lifecycleMu.Unlock()
		a.runtimeLogs.Close()
		go func() {
			serverErr := a.server.Shutdown(ctx)
			if serverErr != nil {
				serverErr = errors.Join(serverErr, a.server.Close())
			}
			a.workers.Wait()
			for _, client := range a.upstreams {
				client.CloseIdleConnections()
			}
			a.lifecycleMu.Lock()
			workerErr := a.backgroundErr
			a.lifecycleMu.Unlock()
			a.shutdownErr = errors.Join(serverErr, workerErr, a.db.Close())
			close(a.shutdownDone)
		}()
	})
	select {
	case <-a.shutdownDone:
		return a.shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
