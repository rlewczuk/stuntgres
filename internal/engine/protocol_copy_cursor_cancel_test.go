package engine

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Schedule real cancellation between selecting a live input context and the
// cursor's next Err check. The returned context remains an ordinary context:
// its first Err observation is nil, and every later observation is canceled.
type copyCursorCancelContext struct {
	context.Context
	cancel context.CancelCauseFunc
	cause  error
	fired  bool
}

func (c *copyCursorCancelContext) Err() error {
	err := c.Context.Err()
	if err == nil && !c.fired {
		c.fired = true
		c.cancel(c.cause)
	}
	return err
}

func TestProtocolCopyCursorCapturedCancellation(t *testing.T) {
	copyCursorCapturedCancellation(t, false)
}

func TestProtocolCopyBinaryCursorCapturedCancellation(t *testing.T) {
	copyCursorCapturedCancellation(t, true)
}

func copyCursorCapturedCancellation(t *testing.T, binaryInput bool) {
	for _, tc := range []struct {
		name, input, code, message, rowContext string
		cause                                  error
	}{
		{
			name: "invalid_record", input: "bad\n", code: "22P02",
			message:    "invalid input syntax for type integer: \"bad\"",
			rowContext: "COPY t, line 1, column v: \"bad\"",
			cause:      &sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"},
		},
		{
			name: "valid_record", input: "1\n", code: "57014",
			message:    "canceling statement due to user request",
			rowContext: "COPY t, line 1: \"1\"",
			cause:      &sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"},
		},
		{name: "ordinary_cancel", input: "bad\n", cause: context.Canceled},
		{
			name: "terminal_cancel", input: "bad\n", code: "57P01",
			message:    "terminating connection due to administrator command",
			rowContext: "COPY t, line 0",
			cause:      &sqlstate.Error{Severity: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"},
		},
	} {
		if binaryInput {
			payload := []byte{0, 0, 0, 1, 0}
			if tc.name == "valid_record" {
				payload = payload[:4]
				tc.rowContext = "COPY t, line 1"
			} else if tc.name == "invalid_record" {
				tc.code, tc.message = "22P03", "incorrect binary data format"
				tc.rowContext = "COPY t, line 1, column v"
			} else if tc.name == "terminal_cancel" {
				tc.rowContext = ""
			}
			// The binary header is already consumed: cancellation is scheduled
			// at admitted row decoding, not during CopyFromBinaryStart.
			stream := []byte{0, 1, 0, 0, 0, byte(len(payload))}
			tc.input = string(append(stream, payload...))
		}
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			session := sessionStart(t, e, nil)
			source, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			op, _, err := session.AcquireCommand(source)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			scope.protocol().command = true
			defer scope.Finish(context.Canceled)
			var cursor exec.RowCursor
			binding := analyze.Binding{
				Relation: &catalog.Relation{ObjectName: "t", Columns: []catalog.Column{{Name: "v", Usage: testUsage(e, sqltype.Int4)}}},
				Targets:  []int{0},
			}
			cursor = dml.NewCopyCursor(scope.writeHost(), binding, copyfmt.NewLineReader(
				copyfmt.Options{Delimiter: '\t', Null: "\\N"},
				bufio.NewReader(strings.NewReader(tc.input)),
				[]copyfmt.Target{{Name: "v"}},
			), nil)
			if binaryInput {
				// Consume a valid file header (no flags, no extension) before
				// Next, so the cursor starts at the tuple with its header done.
				header := copyfmt.BinarySignature + "\x00\x00\x00\x00\x00\x00\x00\x00"
				in := copyfmt.NewBinaryReader(bufio.NewReader(strings.NewReader(header + tc.input)))
				if err := in.ReadHeader(); err != nil {
					t.Fatal("binary header setup", err)
				}
				cursor = dml.NewCopyBinaryCursor(scope.writeHost(), binding, in)
			}
			op.CopyInput.Active.Store(true)
			op.CopyInput.Decoding.Store(true)
			defer op.StopCopyInput()
			scheduled := &copyCursorCancelContext{Context: op.Ctx, cancel: cancel, cause: tc.cause}
			op.Ctx = scheduled
			present, err := cursor.Next(context.Background(), make([]sqltype.Value, 1))
			if !scheduled.fired || present || err == nil {
				t.Fatalf("scheduled cancellation fired=%v row=%v err=%v", scheduled.fired, present, err)
			}
			if tc.code == "" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary cancellation changed identity", err)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Context != tc.rowContext {
					t.Fatalf("cursor cancellation diagnostic=%+v err=%v want %s %q %q", diagnostic, err, tc.code, tc.message, tc.rowContext)
				}
			}
			if cause, ok := tc.cause.(*sqlstate.Error); ok && cause.Context != "" {
				t.Fatal("cursor mutated original cancellation cause", cause)
			}
			if context.Cause(source) != tc.cause || source.Err() != context.Canceled {
				t.Fatal("cursor lost genuine source cancellation authority", context.Cause(source))
			}
		})
	}
}

func TestProtocolCopyStartCancellationAuthority(t *testing.T) {
	primary := errors.New("COPY Start transport failure")
	for _, tc := range []struct {
		name, input, code, message, rowContext string
		cause                                  error
		before, startFailure                   bool
	}{
		{name: "ordinary_cancel", input: "bad\n", cause: context.Canceled},
		{
			name: "terminal_cancel", input: "bad\n", code: "57P01",
			message: "terminating connection due to administrator command",
			cause:   &sqlstate.Error{Severity: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"},
		},
		{
			name: "before_admission", input: "bad\n", before: true, code: "57014",
			message: "canceling statement due to user request",
			cause:   &sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"},
		},
		{
			name: "primary_start_failure", input: "bad\n", startFailure: true,
			cause: &sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			session := sessionStart(t, e, nil)
			sessionSQL(t, session, "CREATE TABLE t(v integer)")
			source, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			op, _, err := session.AcquireCommand(source)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			scope.protocol().command = true
			reader := strings.NewReader(tc.input)
			called := false
			if tc.before {
				cancel(tc.cause)
			}
			count, err := scope.CopyProtocol(copyUnitSyntax(), reader, nil, CopyProtocolHooks{Start: func(meta CopyProtocolMetadata) error {
				called = true
				if !meta.From || meta.Format != 0 || len(meta.ColumnFormats) != 1 || meta.ColumnFormats[0] != 0 {
					t.Fatal("incorrect actual COPY Start metadata", meta)
				}
				cancel(tc.cause)
				if tc.startFailure {
					return primary
				}
				return nil
			}})
			err = scope.copyCommandError(err)
			if count != 0 || called == tc.before || len(scope.stage.rows) != 0 {
				t.Fatal("COPY Start cancellation lost admission or staged rows", count, called, err)
			}
			if tc.startFailure {
				if err != primary {
					t.Fatal("Start primary failure replaced by cancellation", err)
				}
			} else if tc.code == "" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary Start cancellation changed identity", err)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Context != tc.rowContext {
					t.Fatalf("Start cancellation diagnostic=%+v err=%v want %s %q %q", diagnostic, err, tc.code, tc.message, tc.rowContext)
				}
			}
			if reader.Len() != len(tc.input) {
				t.Fatal("Start cancellation authority consumed input", reader.Len(), tc.name)
			}
			if op.CopyInput.Active.Load() || op.CopyInput.Decoding.Load() || op.CopyInput.Masked != nil {
				t.Fatal("COPY input ownership escaped Start/decoder lifetime")
			}
			if context.Cause(source) != tc.cause || source.Err() != context.Canceled {
				t.Fatal("COPY Start changed source cancellation authority", context.Cause(source))
			}
			if cause, ok := tc.cause.(*sqlstate.Error); ok && cause.Context != "" {
				t.Fatal("COPY Start mutated cancellation cause", cause)
			}
		})
	}
}
