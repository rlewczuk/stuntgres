package engine

import (
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

type protocolCopyScopeWriter func([]byte) (int, error)

func (w protocolCopyScopeWriter) Write(p []byte) (int, error) { return w(p) }

func TestProtocolCopyQuerySignatureScope(t *testing.T) {
	for _, test := range []struct {
		name             string
		wire, innerError bool
	}{
		{"embedded_output_error", false, false},
		{"embedded_inner_analysis_error", false, true},
		{"protocol_output_error", true, false},
		{"protocol_inner_analysis_error", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, _, op, scope := copyUnitStart(t, false)
			defer op.Done()
			SetProtocolScopeCommand(ScopeProtocol(scope), test.wire)
			var commandErr error
			defer func() { _ = scope.Finish(commandErr) }()
			outer := &StatementDescription{Parameters: analyze.ParameterInfo{Count: 1, Types: []sqltype.Usage{testUsage(e, sqltype.Int4)}}}
			scope.Expected = outer
			sql := "COPY (SELECT 1) TO STDOUT"
			if test.innerError {
				sql = "COPY (SELECT missing) TO STDOUT"
			}
			primary := errors.New("output stopped")
			var lease chan struct{}
			writer := protocolCopyScopeWriter(func([]byte) (int, error) {
				lease = scope.Owner.CopyDone
				if scope.foreignTablesOrZero().cursor != nil {
					t.Error("COPY retained its producer's borrowed scope during output")
				}
				if scope.Expected != outer {
					t.Error("inner query signature escaped into COPY output")
				}
				if !scope.Owner.Admission.TryLock() {
					t.Error("COPY output held owner admission")
				} else {
					scope.Owner.Admission.Unlock()
				}
				if !e.Mu.TryLock() {
					t.Error("COPY output held engine mutex")
				} else {
					e.Mu.Unlock()
				}
				return 0, primary
			})
			_, commandErr = scope.CopyProtocol(statement(t, sql).(*parser.Copy), nil, writer, CopyProtocolHooks{})
			if scope.Expected != outer {
				t.Fatal("COPY did not restore caller signature after failure")
			}
			if ProtocolScopeBind(ScopeProtocolOrZero(scope)) != nil || scope.foreignTablesOrZero().cursor != nil {
				t.Fatal("COPY left borrowed preparation or producer authority attached")
			}
			if !test.innerError {
				if lease == nil || scope.Owner.CopyDone != lease || scope.Owner.CopyLease != ScopeLeaseRef(scope) {
					t.Fatal("query disposal replaced or released COPY's active reservation")
				}
				select {
				case <-lease:
					t.Fatal("query disposal completed the COPY reservation before scope finish")
				default:
				}
			}
			if test.innerError {
				if commandErr == nil {
					t.Fatal("inner analyzer did not exercise failure cleanup")
				}
			} else if !errors.Is(commandErr, primary) {
				t.Fatal("outer NoData/formals constrained inner query or output error was lost", commandErr)
			}
		})
	}
}
