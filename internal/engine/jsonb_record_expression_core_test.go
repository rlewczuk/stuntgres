package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestJSONBRecordSourceProjection(t *testing.T) {
	e, source, _ := typedUnitCatalog(t, false)
	view := e.CatalogRead()
	original, _ := view.LookupType(source.RowType)
	jsonb, _ := view.LookupType(sqltype.JSONB)
	document, err := jsonb.InputWithEnvironment(context.Background(), `{}`, -1, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	callFor := func(typ *sqltype.Descriptor) functions.JSONBRecord {
		call := functions.JSONBRecord{
			Info: expr.Info{Usage: typ.Usage(), Pos: parser.Position{Offset: 40}},
			Name: "jsonb_populate_record",
		}
		args := []expr.Expr{
			plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: sqltype.Null(typ)},
			plan.LiteralExpr{Info: expr.Info{Usage: jsonb.Usage()}, Value: document},
		}
		next, err := call.Reconstruct(call.Info, args)
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	column := func(name string, slot int, usage sqltype.Usage) analyze.QueryColumn {
		return analyze.QueryColumn{Descriptor: catalog.Column{Name: name, Usage: usage}, Slot: slot, Leaf: -1, Physical: -1}
	}
	first := original.CompositeMember(0)
	second := original.CompositeMember(1)
	left := column("other", 0, first.Usage)
	oldSource := []analyze.QueryColumn{column("x", 5, first.Usage), column("y", 6, second.Usage)}
	oldColumns := []analyze.QueryColumn{left, oldSource[0], oldSource[1], column("n", 7, first.Usage)}
	star := parser.StarExpr{Pos: parser.Position{Offset: 7}}
	b := analyze.Binder{Host: e.AnalysisHost(nil), Query: analyze.QueryScopeState{Auth: &AuthorizationView{View: access.View{Catalog: view}}}}
	out, err := b.JSONBRecordCallStar(oldColumns, oldSource, star, callFor(original))
	if err != nil || len(out) != 4 {
		t.Fatal("initial star", out, err)
	}
	key := analyze.PreparedTypeKey{Offset: 40, Kind: analyze.CompositeSourceExpansion, Projection: 7}
	captured := b.Prepared.Types[key]
	if err := stored.ValidateCompositeOccurrence(captured, view); err != nil {
		t.Fatal(err)
	}
	oldSource[0].Descriptor.Name = "changed after capture"
	if captured.Names[0] != "x" || captured.Attributes[0] != first.Attribute || captured.Attributes[1] != second.Attribute {
		t.Fatal("capture borrowed mutable labels or lost stable attributes", captured)
	}
	b.Expected = ExpectedOf(NewDescription(DescriptionFields{CatalogOwner: view.Owner(), CatalogEpoch: view.Epoch(), Types: b.Prepared.Types}))
	b.Prepared.Types = nil
	current, err := sqltype.Composite(original.ID(), original.Name(), original.SQLName(), original.AssociatedArray(), []sqltype.CompositeMember{first, second, {Attribute: 99, Name: "extra", Usage: first.Usage}})
	if err != nil {
		t.Fatal(err)
	}
	currentSource := []analyze.QueryColumn{column("new_x", 5, first.Usage), column("new_y", 6, second.Usage), column("extra", 7, first.Usage)}
	ordinal := column("n", 8, first.Usage)
	currentColumns := []analyze.QueryColumn{left, currentSource[0], currentSource[1], currentSource[2], ordinal}
	call := callFor(current)
	node := &plan.FunctionFromNode{Calls: []expr.Expr{call}, Ordinality: true, Frame: 3}
	qualifier := analyze.QueryQualifier{SourceRoot: node, Columns: append(append([]analyze.QueryColumn(nil), currentSource...), ordinal)}
	out, err = b.JSONBRecordStarColumns(currentColumns, 3, star, &qualifier)
	if err != nil || len(out) != 4 {
		t.Fatal("retained projection", out, err)
	}
	for i, want := range []struct {
		name string
		slot int
	}{{"other", 0}, {"x", 5}, {"y", 6}, {"n", 8}} {
		if out[i].Descriptor.Name != want.name || out[i].Slot != want.slot {
			t.Fatal("unrelated slot, alias, or ordinal changed", out)
		}
	}
	if call.ResultUsage().Type != current || exec.FunctionFromCallWidth(node.Calls[0]) != 3 || len(currentColumns) != 5 {
		t.Fatal("projection truncated current source authority")
	}
	// A second lexical star has its own analyzed target list.
	otherStar := parser.StarExpr{Pos: parser.Position{Offset: 20}}
	out, err = b.JSONBRecordStarColumns(currentColumns, 3, otherStar, &qualifier)
	if err != nil || len(out) != 5 {
		t.Fatal("star occurrences aliased", out, err)
	}
	for _, tc := range []struct {
		members []sqltype.CompositeMember
		message string
	}{
		{[]sqltype.CompositeMember{first}, "attribute 4 of type record has been dropped"},
		{nil, "attribute 2 of type record has been dropped"},
	} {
		retired, err := sqltype.Composite(original.ID(), original.Name(), original.SQLName(), original.AssociatedArray(), tc.members)
		if err != nil {
			t.Fatal(err)
		}
		live := make([]analyze.QueryColumn, len(tc.members))
		for i, member := range tc.members {
			live[i] = column(member.Name, 5+i, member.Usage)
		}
		out, err := b.JSONBRecordCallStar(live, live, star, callFor(retired))
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: tc.message}
		if out != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal("retired source target lost stable identity or diagnostic", out, err)
		}
	}
}

func TestJSONBRecordArgumentIdentity(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ base, want string }{
		{`NULL::pair`, "(1,)"},
		{`'(9,keep)'::pair`, "(1,keep)"},
		{`ROW(9,'keep')::pair`, "(1,keep)"},
		{`CAST(NULL::pair AS pair)`, "(1,)"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			engine, source, _ := typedUnitCatalog(t, false)
			view := engine.CatalogRead()
			original, _ := view.LookupType(source.RowType)
			first := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
			raw := statement(t, `SELECT jsonb_populate_record(`+tc.base+`,'{"id":1}'::jsonb)`).(*parser.Select).Targets[0].(parser.FunctionExpr)
			if _, err := first.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err != nil {
				t.Fatal(err)
			}
			if first.Clause.JSONBRecordBase || len(first.Prepared.Types) == 0 {
				t.Fatal("base capture escaped or was absent")
			}
			specialized := 0
			for _, captured := range first.Prepared.Types {
				if captured.Kind != analyze.CompositeRecordArgument {
					continue
				}
				specialized++
				if captured.Base.Type != original {
					t.Fatal("wrong provider capture", captured)
				}
				if err := stored.ValidateCompositeOccurrence(captured, view); err != nil {
					t.Fatal(err)
				}
			}
			if specialized == 0 {
				t.Fatal("specialized record argument authority missing")
			}
			// Use a now-absent spelling at the same analyzed cast occurrences.
			// The real provider remains present under its current catalog name.
			var oldSpelling func(parser.Expr) parser.Expr
			oldSpelling = func(expression parser.Expr) parser.Expr {
				if cast, ok := expression.(parser.CastExpr); ok {
					cast.TypeName.Name = "jbr_previous_pair"
					cast.Child = oldSpelling(cast.Child)
					return cast
				}
				return expression
			}
			raw.Args[0] = oldSpelling(raw.Args[0])
			expected := NewDescription(DescriptionFields{CatalogOwner: view.Owner(), CatalogEpoch: view.Epoch(), Types: first.Prepared.Types})
			next := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, expected)
			node, err := next.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil || next.Clause.JSONBRecordBase {
				t.Fatal("retained base rebound by stale name or leaked scope", err)
			}
			value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: view}})
			if err != nil || value.Type() != original {
				t.Fatal(value, err)
			}
			text, err := value.Type().Output(ctx, value)
			if err != nil || text != tc.want {
				t.Fatal(text, err)
			}
			cast := raw.Args[0].(parser.CastExpr)
			annotation, err := next.JSONBRecordCastAnnotation(cast)
			if err != nil || annotation.Name != "jbr_previous_pair" {
				t.Fatal("capture affected unrelated cast scope", annotation, err)
			}
			next.Clause.JSONBRecordBase = true
			next.Expected.CatalogEpoch++
			annotation, err = next.JSONBRecordCastAnnotation(cast)
			if err != nil || annotation.Name != "jbr_previous_pair" {
				t.Fatal("provider OID crossed catalog epoch", annotation, err)
			}
		})
	}
	engine, _, _ := typedUnitCatalog(t, false)
	b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	raw := statement(t, `SELECT jsonb_populate_record('(bad,x)'::pair,'{}'::jsonb)`).(*parser.Select).Targets[0]
	if _, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err == nil || b.Clause.JSONBRecordBase {
		t.Fatal("failed base analysis leaked its capture scope", err)
	}
}
