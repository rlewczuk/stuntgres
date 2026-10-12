package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"runtime"
)

func runServe(options serveOptions) (err error) {
	ready := false
	defer func() {
		if !ready {
			err = errors.Join(err, notifyReady("", err))
		}
	}()
	logs, err := openLogs(options.logdir, options.verbose)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, logs.Close()) }()
	signals, stopSignals := lifecycleSignals()
	defer stopSignals()
	users, err := loadAccounts(options.userfile, true)
	if err != nil {
		return err
	}
	latest := make(map[string]string)
	if options.dumpdir != "" {
		if err := os.MkdirAll(options.dumpdir, 0700); err != nil {
			return err
		}
		if options.restore {
			latest, err = latestDumps(options.dumpdir)
			if err != nil {
				return err
			}
		}
	}
	restored := make([]string, 0, len(latest))
	for name := range latest {
		restored = append(restored, name)
	}
	databases, err := newDatabases(context.Background(), users, restored)
	if err != nil {
		return err
	}
	if options.restore {
		if err := restoreDatabases(context.Background(), databases, latest, logs); err != nil {
			return err
		}
	}
	if runtime.GOOS == "linux" {
		pid, pidErr := acquirePID(options.pidfile)
		if pidErr != nil {
			return pidErr
		}
		defer func() { err = errors.Join(err, pid.Close()) }()
	} else {
		if err := logs.operational(context.Background(), slog.LevelWarn, "PID and signal controls are unavailable on this platform"); err != nil {
			return err
		}
	}
	srv, err := newServer(context.Background(), users, databases, logs)
	if err != nil {
		return err
	}
	defer srv.shutdown()
	listener, err := net.Listen("tcp", options.listen)
	if err != nil {
		return err
	}
	srv.listener = listener
	if address, ok := listener.Addr().(*net.TCPAddr); ok && !address.IP.IsLoopback() {
		if err := logs.operational(context.Background(), slog.LevelWarn, "WARNING: non-loopback cleartext password authentication without TLS exposes passwords and data", "address", listener.Addr().String()); err != nil {
			return err
		}
	}
	if err := logs.operational(context.Background(), slog.LevelInfo, "server started", "address", listener.Addr().String(), "databases", databaseNames(databases)); err != nil {
		return err
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.serve() }()
	var serveErr error
	stopped, joined, pendingDump := false, false, false
	defer func() {
		srv.stop()
		if !joined {
			err = errors.Join(err, <-serveDone)
		}
	}()
	if err := notifyReady(listener.Addr().String(), nil); err != nil {
		return err
	}
	ready = true
	for !stopped {
		if pendingDump {
			pendingDump = false
			if options.dumpdir != "" {
				if dumpErr := dumpDatabases(context.Background(), databases, options.dumpdir, logs); dumpErr != nil {
					if logErr := logs.operational(context.Background(), slog.LevelError, "dump cycle failed", "error", dumpErr.Error()); logErr != nil {
						srv.fail(logErr)
					}
				}
			}
			// Coalesce all currently pending dump signals into at most one next cycle;
			// shutdown always wins over another dump. No second lifecycle worker exists.
		drain:
			for {
				select {
				case signal := <-signals:
					if isDumpSignal(signal) {
						pendingDump = true
					} else {
						stopped = true
						break drain
					}
				case <-srv.ctx.Done():
					stopped = true
					break drain
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
				stopped = true
			}
		case <-srv.ctx.Done():
			stopped = true
		case serveErr = <-serveDone:
			joined = true
			stopped = true
		}
	}
	srv.shutdown()
	if !joined {
		serveErr = <-serveDone
		joined = true
	}
	err = errors.Join(serveErr, srv.runtimeError())
	if options.dumpdir != "" {
		err = errors.Join(err, dumpDatabases(context.Background(), databases, options.dumpdir, logs))
	}
	err = errors.Join(err, logs.operational(context.Background(), slog.LevelInfo, "server stopped"))
	return err
}
