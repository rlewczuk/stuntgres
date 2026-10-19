package engine

import (
	"context"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPreparedSQLActualNoSnapshotDemand(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	op, err := session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	null := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
	text := func(value string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: value}}
	}
	call := func(name string, args ...parser.Expr) parser.Expr {
		return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
	}
	for _, tc := range []struct {
		name   string
		formal sqltype.ID
		actual parser.Expr
	}{
		{"primitive", sqltype.Text, text("x")},
		{"strict_comment", sqltype.Text, call("obj_description", null, text("pg_class"))},
		{"strict_deparse", sqltype.Text, call("pg_get_serial_sequence", null, text("v"))},
		{"strict_property", sqltype.Bool, call("pg_index_has_property", null, text("clusterable"))},
		{"strict_visibility", sqltype.Bool, call("pg_type_is_visible", null)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.gate(&parser.Select{}); err != nil {
				_ = scope.Finish(err)
				t.Fatal(err)
			}
			formal := ParameterInfo{Count: 1, Types: []sqltype.Usage{testUsage(e, tc.formal)}}
			frame := make([]sqltype.Value, 1)
			err = scope.sqlExecuteImporter(&parser.ExecuteSQL{Name: parser.Identifier{Text: "p"}, Arguments: []parser.Expr{tc.actual}}, false)(op.ctx, formal, frame, scope.input)
			if !e.mu.TryLock() {
				t.Fatal("SQL actual importer retained engine mutex")
			}
			e.mu.Unlock()
			if err != nil {
				_ = scope.Finish(err)
				t.Fatal(err)
			}
			if scope.commentCatalog.State() != nil || scope.deparseAuth.RoleRecord != nil || scope.visibilityCatalog.State() != nil || scope.deparsePath != nil {
				t.Fatal("primitive/dead actual captured catalog environment")
			}
			if frame[0].Type() != formal.Types[0].Type {
				t.Fatal("noncanonical actual frame")
			}
			if tc.name != "primitive" && !frame[0].IsNull() {
				t.Fatal("STRICT actual did not return NULL")
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPreparedSQLActualProgramOwnership(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	op := transactionOperation(t, session)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	var commandErr error
	defer func() { _ = scope.Finish(commandErr) }()
	if commandErr = scope.gate(&parser.Select{}); commandErr != nil {
		t.Fatal(commandErr)
	}
	actual := parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "length"}}},
		Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "owned"}}},
	}
	importer := scope.sqlExecuteImporter(&parser.ExecuteSQL{Name: parser.Identifier{Text: "p"}, Arguments: []parser.Expr{actual}}, true)
	formal := ParameterInfo{Count: 1, Types: []sqltype.Usage{testUsage(e, sqltype.Int4)}}
	frame := make([]sqltype.Value, 1)
	var original []routineAdmission
	for i := range 32 {
		if commandErr = importer(op.Context(), formal, frame, scope.input); commandErr != nil {
			t.Fatal(commandErr)
		}
		clear(frame)
		if len(scope.routinePrograms) != 1 || len(scope.routinePrograms[0].admission.calls) == 0 {
			t.Fatal("actual invocations did not retain one consumed authority graph")
		}
		calls := scope.routinePrograms[0].admission.calls
		if i == 0 {
			original = append([]routineAdmission(nil), calls...)
		}
		if len(calls) != len(original) {
			t.Fatal("repeated actuals retained a hop-sized history")
		}
		for j, call := range calls {
			old := original[j]
			if call.reference != old.reference || call.routine != old.routine || call.consumer != old.consumer || call.actor != old.actor {
				t.Fatal("coalescing replaced original consumed-call ownership")
			}
		}
	}
	foreign := New()
	foreignSession := sessionStart(t, foreign, nil)
	foreignOp := transactionOperation(t, foreignSession)
	defer foreignOp.Done()
	foreign.mu.Lock()
	auth, authErr := foreign.authorizationLocked(foreignOp)
	foreign.mu.Unlock()
	if authErr != nil {
		t.Fatal(authErr)
	}
	if err := scope.routinePrograms[0].admission.readmit(auth); err == nil {
		t.Fatal("coalesced consumed calls accepted a foreign authority")
	}
}
