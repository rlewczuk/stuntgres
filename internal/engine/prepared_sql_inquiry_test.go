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
			e.mu.Lock()
			err = scope.sqlExecuteImporter(&parser.ExecuteSQL{Name: parser.Identifier{Text: "p"}, Arguments: []parser.Expr{tc.actual}})(op.ctx, formal, frame, scope.input)
			e.mu.Unlock()
			if err != nil {
				_ = scope.Finish(err)
				t.Fatal(err)
			}
			if scope.commentCatalog.state != nil || scope.deparseAuth.roleRecord != nil || scope.visibilityCatalog.state != nil || scope.deparsePath != nil {
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
