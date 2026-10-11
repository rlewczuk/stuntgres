package stuntgres

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"runtime"
	"strconv"
	"stuntgres/internal/engine"
	"stuntgres/internal/parser"
)

// CopyResult is returned only after the complete COPY statement succeeds.
// Count excludes a header; CommandTag is COPY followed by the decimal count.
type CopyResult struct {
	Count      int64
	CommandTag string
}

// CopyFrom imports text or CSV COPY FROM STDIN on an already pinned, unwrapped native
// connection. The caller owns source; Read callbacks must not reenter this Conn.
func CopyFrom(ctx context.Context, connection *sql.Conn, statement string, source io.Reader) (CopyResult, error) {
	if source == nil {
		return CopyResult{}, errors.New("stuntgres: COPY source is nil")
	}
	return copyStream(ctx, connection, statement, source, nil, true)
}

// CopyTo exports text or CSV COPY TO STDOUT. A failure may leave an irreversible output
// prefix, but returns no success result. The caller owns destination; Write
// callbacks must not reenter this Conn.
func CopyTo(ctx context.Context, connection *sql.Conn, statement string, destination io.Writer) (CopyResult, error) {
	if destination == nil {
		return CopyResult{}, errors.New("stuntgres: COPY destination is nil")
	}
	return copyStream(ctx, connection, statement, nil, destination, false)
}

var errCopyCallbackPanic = errors.New("stuntgres: COPY callback panicked")

func copyStream(ctx context.Context, connection *sql.Conn, statement string, source io.Reader, destination io.Writer, from bool) (result CopyResult, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	if connection == nil {
		return result, errors.New("stuntgres: COPY connection is nil")
	}
	var interrupted any
	err = connection.Raw(func(raw any) (rawErr error) {
		// Raw retires its connection on an escaping panic. Native request cleanup
		// has already failed Finish before rethrowing; transport that panic past
		// Raw's normal release, then restore it outside the Raw callback.
		defer func() {
			if value := recover(); value != nil {
				interrupted = value
				rawErr = errCopyCallbackPanic
			}
		}()
		native, ok := raw.(*conn)
		if !ok {
			return errors.New("stuntgres: COPY requires an unwrapped native connection")
		}
		var count int64
		count, err = native.copyRequest(ctx, statement, source, destination, from)
		if err == nil {
			result = CopyResult{count, "COPY " + strconv.FormatInt(count, 10)}
		}
		return err
	})
	if interrupted != nil {
		panic(interrupted)
	}
	if err != nil {
		result = CopyResult{}
	}
	return
}
func (c *conn) copyRequest(ctx context.Context, statement string, source io.Reader, destination io.Writer, from bool) (count int64, err error) {
	defer runtime.KeepAlive(c)
	if err = c.check(ctx); err != nil {
		return
	}
	op, options, err := c.session.AcquireCommand(ctx)
	if err != nil {
		return 0, c.terminalError(err)
	}
	defer op.Done()
	scope, err := c.session.Engine().StartStatement(op)
	if err != nil {
		return 0, c.terminalError(op.CommandError(err))
	}
	defer func() {
		interrupted := recover()
		if interrupted != nil {
			err = errCopyCallbackPanic
		}
		err = scope.Finish(err)
		err = c.terminalError(op.CommandError(err))
		if err != nil {
			count = 0
		}
		notices := op.DetachNotices()
		op.Done()
		if c.noticeHandler != nil {
			for _, notice := range notices {
				c.noticeHandler(notice)
			}
		}
		if interrupted != nil {
			panic(interrupted)
		}
	}()
	statements, err := parser.ParseWithOptions(statement, options)
	if err != nil {
		return 0, err
	}
	if len(statements) != 1 {
		return 0, errors.New("stuntgres: COPY requires exactly one statement")
	}
	syntax, ok := statements[0].(*parser.Copy)
	if !ok {
		return 0, errors.New("stuntgres: COPY requires a COPY statement")
	}
	if syntax.Endpoint == parser.CopyFile || syntax.Endpoint == parser.CopyProgram {
		return 0, engine.CopyBoundary(syntax)
	}
	if syntax.From != from {
		return 0, errors.New("stuntgres: COPY helper direction does not match statement")
	}
	return scope.Copy(syntax, source, destination)
}
