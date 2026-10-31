package core_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPageinspectLiveCaptureAuthority(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION pageinspect")
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := enginetest.Statement(t, "SELECT brin_page_type(NULL::bytea)").(*parser.Select).Targets[0]
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	call := node.(plan.InstalledPageinspectExpr)
	scope := pgrowlocksUnitScope(t, e, s)
	if _, err := scope.WriteHost().Contrib().Pageinspect(call, 0); err != nil {
		t.Fatal("genuine live pageinspect capture rejected", err)
	}
	foreign, ext := ownershipUnitInstall(t, "pageinspect")
	foreignOwner := foreign.CatalogRead().MustProcedure(ext.Installation.Members[0]).Owner
	for _, mutate := range []func(*plan.InstalledPageinspectExpr){
		func(n *plan.InstalledPageinspectExpr) { n.Ref.Epoch++ },
		func(n *plan.InstalledPageinspectExpr) {
			p := *n.Procedure
			p.Owner = foreignOwner
			n.Procedure, n.RoutineReference = &p, &p
		},
		func(n *plan.InstalledPageinspectExpr) {
			p := *n.Procedure
			installation := *p.Origin.Installation
			origin := *p.Origin
			origin.Installation = &installation
			p.Origin = &origin
			n.Procedure, n.RoutineReference = &p, &p
		},
	} {
		bad := call
		mutate(&bad)
		if _, err := scope.WriteHost().Contrib().Pageinspect(bad, 0); err == nil {
			t.Fatal("forged live pageinspect capture accepted")
		}
	}
	// Exercise the real installed evaluator and statement host. Decoded LSN/XID
	// values and their descriptors must outlive changed input and a later call,
	// not merely compare equal after recomputing an old expression.
	rawHeader := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "page_header"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "\\x"}}}}
	e.Mu.Lock()
	auth, err := core.ScopeWriteAuthorizationLocked(scope, e)
	var bound expr.Expr
	if err == nil {
		headerBinder := core.NewExpressionBinder(scope.Op.Context(), e, scope.Input, scope.Op, nil)
		headerBinder.Query.Auth = &auth
		headerBinder.Clause.AggregateClause = "SELECT"
		bound, err = headerBinder.BindFunctionProvider(rawHeader)
		if err == nil {
			scope.RoutineAuth, scope.RoutineDependencies = auth, headerBinder.Dependencies
		}
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	header := bound.(plan.InstalledPageinspectExpr)
	page := make([]byte, 8192)
	binary.LittleEndian.PutUint32(page, 0x01234567)
	binary.LittleEndian.PutUint32(page[4:], 0x89abcdef)
	binary.LittleEndian.PutUint32(page[20:], 0xffffffff)
	bytea := header.Args[0].ResultUsage()
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Catalog: auth.Catalog, Host: core.HostOf(scope), Input: scope.Input, Output: scope.Output}}
	evaluate := func(raw []byte) sqltype.Value {
		t.Helper()
		valueInput, err := sqltype.Binary(bytea.Type, string(raw))
		if err != nil {
			t.Fatal(err)
		}
		argument := plan.LiteralExpr{Info: expr.Info{Usage: bytea}, Value: valueInput}
		next, err := header.Rebuild(header.Info, []expr.Expr{argument})
		if err != nil {
			t.Fatal(err)
		}
		value, err := next.Eval(evaluation)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := evaluate(page)
	lsnType, xidType := header.Usage.Type.CompositeMember(0).Usage.Type, header.Usage.Type.CompositeMember(8).Usage.Type
	clear(page)
	second := evaluate(bytes.Repeat([]byte{255}, 8192))
	if first.Type() != header.Usage.Type || first.CompositeField(0).Type() != lsnType || first.CompositeField(8).Type() != xidType {
		t.Fatal("decoded result lost exact descriptor authority")
	}
	firstLSN, err := first.CompositeField(0).PGLSN()
	if err != nil || firstLSN != 0x0123456789abcdef {
		t.Fatal("retained LSN changed", firstLSN, err)
	}
	firstXID, err := first.CompositeField(8).XID()
	if err != nil || firstXID != 0xffffffff {
		t.Fatal("retained XID changed", firstXID, err)
	}
	secondLSN, err := second.CompositeField(0).PGLSN()
	if err != nil || secondLSN != 0xffffffffffffffff {
		t.Fatal("fresh LSN did not read changed bytes", secondLSN, err)
	}
	if value, err := header.Args[0].Eval(evaluation); err != nil {
		t.Fatal(err)
	} else if raw, err := value.Binary(); err != nil || raw != "" {
		t.Fatal("rebuild changed captured predecessor input", raw, err)
	}
}
