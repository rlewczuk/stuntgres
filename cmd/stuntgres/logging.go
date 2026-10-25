package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// serverLogs serializes both sinks, including their shared stdout mirror and
// closure. Calls use Handler.Handle directly because slog.Logger discards I/O
// errors returned by handlers.
type serverLogs struct {
	mu       sync.Mutex
	ops      slog.Handler
	sql      slog.Handler
	queries  slog.Handler // nil unless the query log is enabled
	files    []io.Closer
	closed   bool
	closeErr error
}

// logMirrors attempts every destination even if an earlier write failed.
// serverLogs.mu protects whole JSON records across both handlers.
type logMirrors []io.Writer

func (writers logMirrors) Write(p []byte) (int, error) {
	var result error
	for _, w := range writers {
		n, err := w.Write(p)
		if n != len(p) && err == nil {
			err = io.ErrShortWrite
		}
		result = errors.Join(result, err)
	}
	if result != nil {
		return 0, result
	}
	return len(p), nil
}

func logHandler(w io.Writer, verbose bool) slog.Handler {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) != 0 {
				return attr
			}
			switch attr.Key {
			case slog.TimeKey:
				if attr.Value.Kind() == slog.KindTime {
					return slog.String("tstamp", attr.Value.Time().Format(time.RFC3339))
				}
			case slog.LevelKey:
				attr.Key = "severity"
			case slog.MessageKey:
				attr.Key = "message"
			}
			return attr
		},
	})
}

func openLogs(logdir string, verbose, querylog bool) (*serverLogs, error) {
	logs := &serverLogs{}
	var operational, sql io.Writer = os.Stdout, os.Stdout
	if querylog && logdir == "" {
		return nil, errors.New("query log requires a log directory")
	}
	if logdir != "" {
		if err := os.MkdirAll(logdir, 0700); err != nil {
			return nil, err
		}
		if err := os.Chmod(logdir, 0700); err != nil {
			return nil, err
		}
		names := []string{"server.log", "error.log"}
		if querylog {
			names = append(names, "query.log")
		}
		for _, name := range names {
			file, err := os.OpenFile(filepath.Join(logdir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return nil, errors.Join(err, logs.Close())
			}
			logs.files = append(logs.files, file)
			if err := file.Chmod(0600); err != nil {
				return nil, errors.Join(err, logs.Close())
			}
			switch name {
			case "server.log":
				operational = logMirrors{file, os.Stdout}
			case "error.log":
				sql = logMirrors{file, os.Stdout}
			default:
				// Responses can be large, so the query log is never mirrored.
				logs.queries = logHandler(file, false)
			}
		}
	}
	logs.ops = logHandler(operational, verbose)
	logs.sql = logHandler(sql, verbose)
	return logs, nil
}

func (logs *serverLogs) handle(ctx context.Context, handler slog.Handler, level slog.Level, message string, args ...any) error {
	logs.mu.Lock()
	defer logs.mu.Unlock()
	if logs.closed {
		return os.ErrClosed
	}
	if !handler.Enabled(ctx, level) {
		return nil
	}
	record := slog.NewRecord(time.Now(), level, message, 0)
	record.Add(args...)
	return handler.Handle(ctx, record)
}

func (logs *serverLogs) operational(ctx context.Context, level slog.Level, message string, args ...any) error {
	return logs.handle(ctx, logs.ops, level, message, args...)
}

func (logs *serverLogs) query(ctx context.Context, args ...any) error {
	return logs.handle(ctx, logs.queries, slog.LevelInfo, "query", args...)
}

func (logs *serverLogs) sqlError(ctx context.Context, query string, err error, parameters []loggedParameter, args ...any) error {
	d := diagnostic(err)
	if d == nil {
		return errors.New("cannot log a nil SQL error")
	}
	kind := "SQL"
	switch d.Code {
	case "42601":
		kind = "SYNTAX"
	case "0A000":
		kind = "UNSUPPORTED"
	}
	if parameters == nil {
		parameters = []loggedParameter{}
	}
	args = append(args, "kind", kind, "query", query, "params", parameters, "error", d.Code, "errmsg", d.Message)
	return logs.handle(ctx, logs.sql, slog.LevelError, d.Message, args...)
}

func (logs *serverLogs) Close() error {
	logs.mu.Lock()
	defer logs.mu.Unlock()
	if !logs.closed {
		logs.closed = true
		for _, file := range logs.files {
			logs.closeErr = errors.Join(logs.closeErr, file.Close())
		}
	}
	return logs.closeErr
}

// diagnostic keeps native diagnostics intact. Foreign SQLState errors have no
// trustworthy optional PostgreSQL fields, so only their code/message are used.
func diagnostic(err error) *sqlstate.Error {
	if err == nil {
		return nil
	}
	var native *sqlstate.Error
	if errors.As(err, &native) {
		return native
	}
	code := "XX000"
	var state interface {
		error
		SQLState() string
	}
	message := err.Error()
	if errors.As(err, &state) {
		code = state.SQLState()
		message = state.Error()
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = "57014"
	}
	return &sqlstate.Error{Code: code, Message: message}
}

type loggedParameter struct {
	Index    int     `json:"index"`
	OID      uint32  `json:"oid,omitempty"`
	Type     string  `json:"type,omitempty"`
	Format   string  `json:"format"`
	Null     bool    `json:"null"`
	Encoding string  `json:"encoding"`
	Value    *string `json:"value"`
}

func logParameters(parameters []session.ProtocolLogParameter) []loggedParameter {
	out := make([]loggedParameter, len(parameters))
	for i, p := range parameters {
		entry := loggedParameter{Index: i + 1, OID: p.OID, Type: p.Type, Format: "text", Null: p.Null, Encoding: "text"}
		if p.Format != 0 {
			entry.Format = "binary"
		}
		if !p.Null {
			value := string(p.Bytes)
			if !p.Decoded && (p.Format != 0 || !utf8.Valid(p.Bytes)) {
				entry.Encoding = "base64"
				value = base64.StdEncoding.EncodeToString(p.Bytes)
			}
			entry.Value = &value
		}
		out[i] = entry
	}
	return out
}
