package enginetests_test

import (
	"context"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestStringVariadicRetainedGenerationStoredOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	// Retagging owns a detached tree and exact new descriptors, never changing
	// the source generation. An equal OID cannot authorize capture adoption.
	ctx := context.Background()
	binder := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	bound, err := binder.Bind(nullCountRaw("quote_literal", parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 42}}), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	node := bound.(functions.StringVariadic)
	oldGroup := exec.FoldCaptureCandidate{}
	_, err = exec.FoldPlanningExpression(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, FoldCaptures: &oldGroup}}, node)
	if err != nil || len(oldGroup.Entries) != 1 {
		t.Fatal(err)
	}
	types := preparedFoldRetagTypes()
	retagged, changed, err := stored.RetagTypes(binder.BindingCatalog(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return types[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	fresh := retagged.(functions.StringVariadic)
	if fresh.Usage.Type != types[sqltype.Text] || fresh.Args[0].ResultUsage().Type != types[sqltype.Int4] || node.Usage.Type == fresh.Usage.Type || node.Args[0].ResultUsage().Type == fresh.Args[0].ResultUsage().Type {
		t.Fatal("retag source mutated or authority not replaced")
	}
	candidate := exec.FoldCaptureCandidate{Entries: oldGroup.Entries}
	_, err = exec.FoldPlanningExpression(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, FoldCaptures: &candidate}}, fresh)
	if err != nil || !candidate.Private || len(candidate.Entries) != 1 || candidate.Entries[0].Value.Type() != types[sqltype.Text] || oldGroup.Entries[0].Value.Type() != node.Usage.Type {
		t.Fatal("coincident OID adopted or source group mutated", err)
	}

	// Two DEFAULT definitions, a view, and statement syntax can have identical
	// offset/frame/kind/usage without sharing an occurrence or captured result.
	enginetest.SessionSQL(t, s, "CREATE TABLE sv_quote_origins(a text DEFAULT quote_ident('a'),b text DEFAULT quote_ident('b'))")
	enginetest.SessionSQL(t, s, "CREATE VIEW sv_quote_origin_view AS SELECT quote_ident('v') AS v")
	table := preparedFoldStoredRelation(t, e, "sv_quote_origins")
	view := preparedFoldStoredRelation(t, e, "sv_quote_origin_view")
	catalog := e.CatalogRead()
	tableRef := e.Catalog.Domain.Ref(catalog.Epoch(), table.Address())
	viewRef := e.Catalog.Domain.Ref(catalog.Epoch(), view.Address())
	origins := []exec.FoldOrigin{
		{Owner: tableRef, Attribute: table.Attributes[0], Definition: table.ColumnMeta.Defaults[table.Attributes[0]].OID},
		{Owner: tableRef, Attribute: table.Attributes[1], Definition: table.ColumnMeta.Defaults[table.Attributes[1]].OID},
		{Owner: viewRef, Definition: stored.ViewOf(view).RuleOID(), Revision: stored.ViewOf(view).DefinitionRevision()},
		{},
	}
	stamped := node.WithOrigin(origins[2])
	targetRef := viewRef
	targetRef.Epoch++
	remapped, err := stored.RemapExpression(stamped, func(ref ids.ObjectRef) ids.ObjectRef {
		if ref == viewRef {
			return targetRef
		}
		return ref
	}, nil)
	if err != nil || exec.FoldOriginOf(remapped.(functions.StringVariadic)).Owner != targetRef || exec.FoldOriginOf(stamped) != origins[2] {
		t.Fatal("view quote origin remap lost target ownership or mutated source", err)
	}
	stampedRetagged, _, err := stored.RetagTypes(binder.BindingCatalog(), stamped, func(d *sqltype.Descriptor) *sqltype.Descriptor { return types[d.ID()] })
	if err != nil || stampedRetagged.(functions.StringVariadic).Origin != stamped.Origin {
		t.Fatal("view quote origin lost during typed retag", err)
	}
	collision := exec.FoldCaptureCandidate{}
	for i, origin := range origins {
		input := string(rune('a' + i))
		source := plan.LiteralExpr{Info: expr.Info{Usage: node.Usage}, Value: sqltype.String(node.Usage.Type, input)}
		occurrence := functions.StringVariadic{Info: expr.Info{Usage: node.Usage, Pos: parser.Position{Offset: 17}}, Name: "quote_ident", Frame: 1, Args: []expr.Expr{source}}
		ambient := origin
		if origin.Owner != (ids.ObjectRef{}) && origin.Attribute == 0 {
			occurrence = occurrence.WithOrigin(origin)
			ambient = exec.FoldOrigin{} // Lowered view nodes override caller-root context.
		}
		folded, err := exec.FoldPlanningExpression(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, FoldCaptures: &collision, FoldOrigin: ambient}}, occurrence)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := folded.Text()
		if got != input || len(collision.Entries) != i+1 {
			t.Fatal("distinct stored origins collided", got, input, collision.Entries)
		}
	}
	published := collision.Entries
	stableOrigins := exec.FoldCaptureCandidate{Entries: published}
	stored.PruneFoldCaptures(&stableOrigins, e.CatalogRead())
	if stableOrigins.Private || &stableOrigins.Entries[0] != &published[0] {
		t.Fatal("valid origin pruning copied group")
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE sv_quote_origins RENAME COLUMN a TO renamed")
	stored.PruneFoldCaptures(&stableOrigins, e.CatalogRead())
	if stableOrigins.Private {
		t.Fatal("unrelated rename invalidated DEFAULT output")
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE sv_quote_origins ALTER COLUMN renamed SET DEFAULT quote_ident('new')")
	retired := exec.FoldCaptureCandidate{Entries: published}
	stored.PruneFoldCaptures(&retired, e.CatalogRead())
	if !retired.Private || len(retired.Entries) != 3 || len(published) != 4 || retired.Entries[0].Origin != origins[1] {
		t.Fatal("default replacement retained old origin or mutated publication")
	}
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE VIEW sv_quote_origin_view AS SELECT quote_ident('changed') AS v")
	stored.PruneFoldCaptures(&retired, e.CatalogRead())
	if len(retired.Entries) != 2 || len(published) != 4 {
		t.Fatal("view replacement retained prior defining revision")
	}
	sealed := exec.FoldCaptureCandidate{Entries: published, Sealed: true}
	missing := functions.StringVariadic{Info: expr.Info{Usage: node.Usage, Pos: parser.Position{Offset: 19}}, Name: "quote_ident", Frame: 1, Args: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: node.Usage}, Value: sqltype.String(node.Usage.Type, "local")}}}
	_, err = exec.FoldPlanningExpression(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, FoldCaptures: &sealed}}, missing)
	if err != nil || sealed.Private || len(sealed.Entries) != 4 {
		t.Fatal("sealed executor group admitted row-time publication", err)
	}

}

func preparedFoldRetagTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, descriptor := range sqltype.Builtins() { types[descriptor.ID()] = descriptor }
	return types
}

func preparedFoldStoredRelation(t *testing.T, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	return e.CatalogRead().MustRelation(address)
}
