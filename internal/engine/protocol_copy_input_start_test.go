package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestProtocolCopyInputStartOwnership(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		wire, before, failed bool
		input, code, detail  string
	}{
		{"invalid_after_start", true, false, false, "bad\n", "22P02", "COPY t, line 1, column v: \"bad\""},
		{"valid_after_start", true, false, false, "1\n", "57014", "COPY t, line 1: \"1\""},
		{"embedded_after_start", false, false, false, "bad\n", "", ""},
		{"canceled_before_start", true, true, false, "bad\n", "", ""},
		{"failed_start", true, false, true, "bad\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, op, scope := copyUnitStart(t, false)
			defer op.Done()
			scope.protocol().command = tc.wire
			cause := &sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"}
			veto := errors.New("CopyIn publication failed")
			if tc.before {
				op.Cancel(cause)
			}
			called := false
			count, err := scope.CopyProtocol(copyUnitSyntax(), strings.NewReader(tc.input), nil, CopyProtocolHooks{Start: func(CopyProtocolMetadata) error {
				called = true
				// Cancel in the actual publication callback, before copyIO's
				// resume check. No scheduler timing or socket speed is involved.
				op.Cancel(cause)
				if tc.failed {
					return veto
				}
				return nil
			}})
			if called == tc.before || count != 0 || err == nil {
				t.Fatal("start admission or cancellation lost", called, count, err)
			}
			if tc.failed {
				if !errors.Is(err, veto) {
					t.Fatal("start failure lost precedence", err)
				}
			} else if tc.code != "" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Context != tc.detail {
					t.Fatal("input ownership lost diagnostic", err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary or pre-start cancellation changed", err)
			}
			if op.CopyInput.Active.Load() || op.CopyInput.Decoding.Load() || op.CopyInput.Masked != nil || len(scope.stage.rows) != 0 {
				t.Fatal("failed input leaked ownership or applied rows")
			}
			if finish := scope.Finish(err); finish == nil {
				t.Fatal("failed COPY finished successfully")
			}
			if scope.owner.CopyLease != nil {
				t.Fatal("failed COPY retained reservation")
			}
		})
	}
}
