package control

import (
	"context"
	"io"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Outcome is what one executed statement tells the After entries.
type Outcome struct {
	Created ids.ObjectRef // exact ordinary CREATE TABLE outcome; zero for a no-op
}

// Host is the engine's side of one Restore. Restore calls Begin once the
// script is parsed, Start before the first statement, Statement and Execute
// per statement, and Publish after the Finish entries. The host owns the
// restore stage, its session and the atomic publication; control holds no
// engine pointer and no lock.
type Host interface {
	// Begin receives the script and its parsed statements.
	Begin(source string, statements []parser.Statement) error
	// Start opens the operation every step runs under. Restore defers end,
	// so it runs on every exit after a successful Start, after Publish.
	Start(ctx context.Context) (op *session.Operation, end func(), err error)
	// Statement resets the per-statement state before statement index.
	Statement(index int)
	// Execute runs one admitted statement and closes its rows.
	Execute(op *session.Operation, stmt parser.Statement) (Outcome, error)
	// Publish installs the restored catalog.
	Publish(ctx context.Context, op *session.Operation) error
}

// Restore replays a Dump script through host. It reads and parses src, with
// the Decode entries reading line comments, then per statement walks the
// Before, Admit and Prepare entries, executes the statement and walks the
// After entries; then it walks the Finish entries and publishes. The first
// error stops the replay and skips publication.
func Restore[R any](ctx context.Context, host Host, hooks []Hook[R], run R, src io.Reader) error {
	data, err := io.ReadAll(contextReader{ctx, src})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	source := string(data)
	statements, err := parser.ParseWithOptions(source, parser.Options{Context: ctx, StandardConformingStrings: true, StatisticsOwnerReplay: true, NotNullInheritReplay: true, LineComment: func(comment string, position parser.Position) error {
		return decodeComment(hooks, run, comment, position)
	}})
	if err != nil {
		return err
	}
	if err := host.Begin(source, statements); err != nil {
		return err
	}
	op, end, err := host.Start(ctx)
	if err != nil {
		return err
	}
	defer end()
	for index, stmt := range statements {
		host.Statement(index)
		consumed, err := runBefore(hooks, run, op, stmt)
		if err != nil {
			return err
		}
		if consumed {
			continue
		}
		admission, err := runAdmit(hooks, run, op, stmt)
		if err != nil {
			return err
		}
		if admission == Consumed {
			continue
		}
		if err := runPrepare(hooks, run, op, stmt); err != nil {
			return err
		}
		outcome, err := host.Execute(op, stmt)
		if err != nil {
			return err
		}
		if err := runAfter(hooks, run, op, stmt, outcome); err != nil {
			return err
		}
	}
	if err := runFinish(ctx, hooks, run, op); err != nil {
		return err
	}
	return host.Publish(ctx, op)
}

// contextReader stops reading the Restore input once ctx is done.
type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}
