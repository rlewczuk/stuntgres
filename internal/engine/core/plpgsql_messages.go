package core

import (
	"errors"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Message operands use the same reached-only SQL execution and output environment
// as all other PL expressions. Filtering happens only after this work completes.
func (s *StatementScope) plMessageOperand(d *catalog.Procedure, f *proc.PLFrame, read catalog.Read, query *parser.Select, raw parser.PLExpression, origin parser.Position, action string) (sqltype.Value, error) {
	value, _, _, err := s.evaluatePLExpression(f, query, read, nil, s.plNoticeOrigin(f, origin, action))
	if err != nil {
		return value, s.plRuntimeError(err, read, d, raw.Start, raw.End, origin, action, true)
	}
	return value, nil
}

func (s *StatementScope) plMessageText(value sqltype.Value, origin plNoticeOrigin) (string, error) {
	noticeStart := len(s.Op.Notices)
	defer func() { s.appendPLNoticeContext(noticeStart, origin) }()
	return value.Type().OutputWithEnvironment(s.executionContext(), value, s.Output)
}

func (s *StatementScope) executePLRaise(d *catalog.Procedure, f *proc.PLFrame, in proc.PLInstruction, read catalog.Read) error {
	raw, checked := in.Syntax.Raise, in.Raise
	var diagnostic sqlstate.Error
	level := session.LevelError
	switch checked.Level {
	case parser.PLRaiseDebug:
		diagnostic.Severity, level = "DEBUG", session.LevelDebug1
	case parser.PLRaiseLog:
		diagnostic.Severity, level = "LOG", session.LevelLog
	case parser.PLRaiseInfo:
		diagnostic.Severity, level = "INFO", session.LevelInfo
	case parser.PLRaiseNotice:
		diagnostic.Severity, level = "NOTICE", session.LevelNotice
	case parser.PLRaiseWarning:
		diagnostic.Severity, level = "WARNING", session.LevelWarning
	default:
		diagnostic.Severity = "ERROR"
	}
	diagnostic.SeverityUnlocalized = diagnostic.Severity
	diagnostic.Code = checked.Code
	condition := raw.Condition.Name.Text
	if raw.Form == parser.PLRaiseSQLState {
		condition = raw.Condition.Code
	}
	messageSet := raw.Form == parser.PLRaiseFormat
	fail := func(err error) error {
		return s.plRuntimeError(err, read, d, parser.Position{}, parser.Position{}, in.Syntax.Pos, "at RAISE", false)
	}
	if messageSet {
		var text strings.Builder
		argument := 0
		for i := 0; i < len(checked.Format); i++ {
			if checked.Format[i] != '%' {
				text.WriteByte(checked.Format[i])
				continue
			}
			if i+1 < len(checked.Format) && checked.Format[i+1] == '%' {
				text.WriteByte('%')
				i++
				continue
			}
			value, err := s.plMessageOperand(d, f, read, checked.Arguments[argument], raw.Arguments[argument], in.Syntax.Pos, "at RAISE")
			argument++
			if err != nil {
				return err
			}
			if value.IsNull() {
				text.WriteString("<NULL>")
				continue
			}
			output, err := s.plMessageText(value, s.plNoticeOrigin(f, in.Syntax.Pos, "at RAISE"))
			if err != nil {
				return fail(err)
			}
			text.WriteString(output)
		}
		diagnostic.Message = text.String()
	}
	// Presence bits preserve empty text; SQLSTATE intentionally uses its released
	// numeric-zero sentinel semantics instead of an ERRCODE presence bit.
	var seen uint16
	if messageSet {
		seen = 1 << parser.PLRaiseMessage
	}
	for i, option := range checked.Options {
		value, err := s.plMessageOperand(d, f, read, option.Query, raw.Options[i].Value, in.Syntax.Pos, "at RAISE")
		if err != nil {
			return err
		}
		if value.IsNull() {
			return fail(SQLError("22004", "RAISE statement option cannot be null"))
		}
		output, err := s.plMessageText(value, s.plNoticeOrigin(f, in.Syntax.Pos, "at RAISE"))
		if err != nil {
			return fail(err)
		}
		if option.Kind == parser.PLRaiseErrcode {
			if diagnostic.Code != "" && diagnostic.Code != "00000" {
				return fail(SQLError("42601", "RAISE option already specified: ERRCODE"))
			}
			diagnostic.Code, err = proc.RaiseSQLState(output)
			if err != nil {
				return fail(err)
			}
			condition = output
			continue
		}
		bit := uint16(1) << option.Kind
		if seen&bit != 0 {
			return fail(SQLError("42601", "RAISE option already specified: "+option.Kind.String()))
		}
		seen |= bit
		switch option.Kind {
		case parser.PLRaiseMessage:
			diagnostic.Message, messageSet = output, true
		case parser.PLRaiseDetail:
			diagnostic.Detail = output
		case parser.PLRaiseHint:
			diagnostic.Hint = output
		case parser.PLRaiseColumn:
			diagnostic.Column = output
		case parser.PLRaiseConstraint:
			diagnostic.Constraint = output
		case parser.PLRaiseDatatype:
			diagnostic.DataType = output
		case parser.PLRaiseTable:
			diagnostic.Table = output
		case parser.PLRaiseSchema:
			diagnostic.Schema = output
		}
	}
	if !messageSet {
		switch {
		case condition != "":
			diagnostic.Message = condition
		case diagnostic.Code != "":
			diagnostic.Message = diagnostic.Code
		case level == session.LevelError:
			diagnostic.Message = "P0001"
		default:
			diagnostic.Message = "00000"
		}
	}
	if diagnostic.Code == "" || diagnostic.Code == "00000" {
		switch level {
		case session.LevelError:
			diagnostic.Code = "P0001"
		case session.LevelWarning:
			diagnostic.Code = "01000"
		default:
			diagnostic.Code = "00000"
		}
	}
	if level == session.LevelError {
		return fail(&diagnostic)
	}
	diagnostic.Context = proc.PLActionContext(d.SQL.Source, s.plRoutineDiagnosticIdentity(read, d), in.Syntax.Pos, "at RAISE")
	s.Op.EmitNotice(level, diagnostic)
	return nil
}

func (s *StatementScope) executePLAssert(d *catalog.Procedure, f *proc.PLFrame, in proc.PLInstruction, read catalog.Read) error {
	enabled, ok := s.Op.Setting("plpgsql.check_asserts")
	if !ok {
		return errors.New("stuntgres: missing PL assertion setting")
	}
	if enabled == "off" {
		return nil
	}
	raw := in.Syntax.Assert
	value, err := s.plMessageOperand(d, f, read, in.Assert.Condition, raw.Condition, in.Syntax.Pos, "at ASSERT")
	if err != nil {
		return err
	}
	boolean, ok := read.LookupType(sqltype.Bool)
	if !ok {
		return errors.New("stuntgres: missing PL condition Boolean type")
	}
	noticeStart := len(s.Op.Notices)
	value, err = s.convertPLValue(value, boolean.Usage())
	s.appendPLNoticeContext(noticeStart, s.plNoticeOrigin(f, in.Syntax.Pos, "at ASSERT"))
	if err != nil {
		return s.plRuntimeError(err, read, d, raw.Condition.Start, raw.Condition.End, in.Syntax.Pos, "at ASSERT", false)
	}
	if !value.IsNull() {
		truth, err := value.Boolean()
		if err != nil {
			return err
		}
		if truth {
			return nil
		}
	}
	message := "assertion failed"
	if in.Assert.Message != nil {
		value, err := s.plMessageOperand(d, f, read, in.Assert.Message, raw.Message, in.Syntax.Pos, "at ASSERT")
		if err != nil {
			return err
		}
		if !value.IsNull() {
			message, err = s.plMessageText(value, s.plNoticeOrigin(f, in.Syntax.Pos, "at ASSERT"))
			if err != nil {
				return s.plRuntimeError(err, read, d, raw.Message.Start, raw.Message.End, in.Syntax.Pos, "at ASSERT", false)
			}
		}
	}
	return s.plRuntimeError(SQLError("P0004", message), read, d, parser.Position{}, parser.Position{}, in.Syntax.Pos, "at ASSERT", false)
}
