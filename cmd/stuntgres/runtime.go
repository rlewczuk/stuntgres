package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"

	"stuntgres/internal/serverref"
	"stuntgres/pgserver"
)

func runServe(options serveOptions) (err error) {
	ready := false
	defer func() {
		if !ready {
			err = errors.Join(err, notifyReady("", err))
		}
	}()
	srv, err := pgserver.Open(context.Background(), pgserver.Config{
		Listen: options.listen, UserFile: options.userfile, DumpDir: options.dumpdir, Restore: options.restore,
		LogDir: options.logdir, LogWriter: os.Stdout, QueryLog: options.querylog, Verbose: options.verbose,
		References: serverref.Specs(options.references), ReferenceTimeout: options.referenceTimeout,
	})
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := srv.Close(); closeErr != nil && !errors.Is(err, closeErr) {
			err = errors.Join(err, closeErr)
		}
	}()
	signals, stopSignals := lifecycleSignals()
	defer stopSignals()
	if err = srv.Prepare(); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		pid, pidErr := acquirePID(options.pidfile)
		if pidErr != nil {
			return errors.Join(pidErr, srv.Close())
		}
		defer func() { err = errors.Join(err, pid.Close()) }()
	} else if err = srv.Log(context.Background(), slog.LevelWarn, "PID and signal controls are unavailable on this platform"); err != nil {
		return errors.Join(err, srv.Close())
	}
	if err = srv.Start(); err != nil {
		return err
	}
	if err = notifyReady(srv.Addr().String(), nil); err != nil {
		// Readiness failure is startup abort, not a graceful final dump/stopped cycle.
		return errors.Join(err, srv.Close())
	}
	ready = true
	pendingDump := false
	for {
		if pendingDump {
			select {
			case <-srv.Done():
				return srv.Err()
			default:
			}
			pendingDump = false
			// The API owns all dump records and persistence. A dump failure alone is
			// nonfatal; a required-log failure completes Server.Done.
			_ = srv.Dump(context.Background())
			// Coalesce currently pending dump signals into at most one next cycle,
			// draining termination/Done first so shutdown wins over that next cycle.
		drain:
			for {
				select {
				case signal := <-signals:
					if isDumpSignal(signal) {
						pendingDump = true
					} else {
						return srv.Shutdown(context.Background())
					}
				case <-srv.Done():
					return srv.Err()
				default:
					break drain
				}
			}
			continue
		}
		select {
		case signal := <-signals:
			if isDumpSignal(signal) {
				pendingDump = true
			} else {
				return srv.Shutdown(context.Background())
			}
		case <-srv.Done():
			return srv.Err()
		}
	}
}
