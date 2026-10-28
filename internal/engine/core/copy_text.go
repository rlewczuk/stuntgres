package core

import (
	"context"
	"errors"
	"io"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Wire cancellation carries an immutable SQL diagnostic through the operation's
// context. Ordinary embedded Go cancellation retains its existing identity.
func (s *StatementScope) copyCommandError(err error) error {
	var primary *sqlstate.Error
	if errors.As(err, &primary) {
		return err
	}
	err = s.Op.CommandError(err)
	if errors.As(err, &primary) || !(s.protocolOrZero().command || s.protocolOrZero().simple) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		cancellation := context.Cause(s.Op.Context())
		if cancellation == nil {
			cancellation = context.Cause(s.Op.Ctx)
		}
		var cause *sqlstate.Error
		if errors.As(cancellation, &cause) && cause != nil && (cause.Code == "57014" || cause.Severity == "FATAL") {
			return cause
		}
	}
	return err
}

// copyOutput writes a COPY TO through the reservation-releasing writer and
// re-checks the statement after the last row.
func (s *StatementScope) copyOutput(rows exec.RowResult, o copyfmt.Options, w io.Writer) (int64, error) {
	protocol := s.protocolOrZero()
	source := copyOutputRows{RowResult: rows, notice: func(d sqlstate.Error) { s.Op.EmitNotice(session.LevelWarning, d) }}
	count, err := copyfmt.WriteRows(s.Op.Context(), copyWriter{s, w}, copyTransportRows{source, s.Op.EffectiveSettings()}, rows.Schema(), o, protocol.command || protocol.simple)
	if err != nil {
		return 0, err
	}
	return count, s.Active()
}

// This send-only wrapper is never installed on the retained RowResult.
type copyOutputRows struct {
	exec.RowResult
	notice func(sqlstate.Error)
}

func (r copyOutputRows) OutputEnvironment() sqltype.OutputEnvironment {
	output := r.RowResult.OutputEnvironment()
	output.XMLNotice = r.notice
	return output
}
