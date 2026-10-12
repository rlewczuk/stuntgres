package main

import (
	"context"

	"stuntgres/internal/engine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// completedStatement owns its finalized rows until the writer closes them.
// Notices and settings are detached transport records, not execution authority.
type completedStatement struct {
	result   engine.Result
	notices  []sqlstate.Error
	settings []parameterValue
}

type messageOutput struct {
	statements []completedStatement
	notices    []sqlstate.Error
	settings   []parameterValue
	err        error
	empty      bool
}

// executeQuery completes all SQL boundaries and releases the operation before
// returning. No socket or logging I/O may run while message admission is held.
func (c *client) executeQuery(ctx context.Context, source string) (out messageOutput) {
	op, options, err := c.session.AcquireCommand(ctx)
	if err != nil {
		out.err = err
		out.settings = c.settingsSnapshot()
		return
	}
	defer op.Done()

	scope, err := c.session.Engine().StartStatement(op)
	if err != nil {
		out.err = op.CommandError(err)
		out.notices = op.DetachNotices()
		out.settings = c.settingsSnapshot()
		return
	}

	batch, finished := false, false
	defer func() {
		// Parsing failures also pass through Finish, so the existing scope
		// authority aborts an explicit transaction just like execution errors.
		if batch {
			out.err = scope.FinishMessage(out.err)
		} else if !finished {
			out.err = scope.Finish(out.err)
		}
		out.err = op.CommandError(out.err)
		out.notices = op.DetachNotices()
		out.settings = c.settingsSnapshot()
		if out.err != nil {
			out.empty = false
		}
	}()

	statements, err := parser.ParseWithOptions(source, options)
	if err != nil {
		out.err = err
		return
	}
	if len(statements) == 0 {
		out.empty = true
		return
	}
	if len(statements) > 1 {
		if err := scope.RetainMessage(); err != nil {
			out.err = err
			return
		}
		batch = true
	}

	out.statements = make([]completedStatement, 0, len(statements))
	for i, statement := range statements {
		if i != 0 {
			next, err := scope.NextStatement()
			if err != nil {
				out.err = err
				return
			}
			scope = next
			finished = false
		}
		result, err := scope.Execute(statement, 0, nil)
		err = scope.Finish(err)
		finished = true
		err = op.CommandError(err)
		if err != nil {
			if result.Rows != nil {
				result.Rows.Close()
			}
			out.err = err
			return
		}
		out.statements = append(out.statements, completedStatement{
			result:   result,
			notices:  op.DetachNotices(),
			settings: c.settingsSnapshot(),
		})
	}
	return
}
