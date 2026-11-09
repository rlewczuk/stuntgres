package tablecmds_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// SQL only constructs and evolves these fixtures. Public rows, metadata and
// diagnostics live in reference/alter and reference/alter/unreadview.
type hiddenViewSnapshot struct {
	owner        *catalog.Relation
	definition   *stored.View
	parts        stored.ViewParts
	root         plan.Node
	source       *plan.SourceNode
	before       plan.SourceNode
	output       []catalog.Column
	columns      []catalog.Column
	sources      []stored.ViewLeaf
	dependencies []catalog.ExpressionDependency
}

func singleHiddenSource(t *testing.T, root plan.Node) *plan.SourceNode {
	t.Helper()
	var source *plan.SourceNode
	plan.VisitCTEQuery(root, func(node plan.Node) {
		if next, ok := node.(*plan.SourceNode); ok {
			if source != nil && source != next {
				t.Fatal("fixture has multiple sources")
			}
			source = next
		}
	}, func(expr.Expr) {}, true)
	if source == nil {
		t.Fatal("fixture has no source")
	}
	return source
}

func captureHiddenView(t *testing.T, e *core.Engine, name string) hiddenViewSnapshot {
	t.Helper()
	ref := enginetest.RoleTableRef(t, e, name)
	owner := e.Catalog.State.MustRelation(ref.Address)
	definition := stored.ViewOf(owner)
	parts := definition.Parts()
	source := singleHiddenSource(t, parts.Query.Root)
	before := *source
	before.QuerySchema = slices.Clone(source.Schema())
	sources := slices.Clone(parts.Sources)
	for i := range sources {
		sources[i].Attributes = slices.Clone(sources[i].Attributes)
		sources[i].Reads = slices.Clone(sources[i].Reads)
		sources[i].SystemColumns = slices.Clone(sources[i].SystemColumns)
	}
	return hiddenViewSnapshot{owner: owner, definition: definition, parts: parts, root: parts.Query.Root, source: source, before: before, output: slices.Clone(parts.Query.Root.Schema()), columns: slices.Clone(owner.Columns), sources: sources, dependencies: slices.Clone(parts.Dependencies)}
}

func (old hiddenViewSnapshot) assertRefresh(t *testing.T, e *core.Engine) {
	t.Helper()
	current := e.Catalog.State.MustRelation(old.owner.Address())
	definition := stored.ViewOf(current)
	source := singleHiddenSource(t, definition.Query().Root)
	if current == old.owner || definition == old.definition || definition.Query().Root == old.root || source == old.source || &source.Schema()[0] == &old.source.Schema()[0] {
		t.Fatal("hidden layout refresh did not create owned backing")
	}
	if old.definition.Query().Root != old.root || !reflect.DeepEqual(old.source, &old.before) || !reflect.DeepEqual(old.root.Schema(), old.output) || !reflect.DeepEqual(old.owner.Columns, old.columns) || !reflect.DeepEqual(old.definition.Sources(), old.sources) || !reflect.DeepEqual(old.definition.Dependencies(), old.dependencies) {
		t.Fatal("hidden layout refresh mutated retained original backing")
	}
	if definition.Syntax() != old.parts.Syntax || definition.RuleOID() != old.parts.Rule || definition.Parts().DefinitionRevision != old.parts.DefinitionRevision || !reflect.DeepEqual(current.Columns, old.columns) || !reflect.DeepEqual(definition.Query().Root.Schema(), old.output) || !reflect.DeepEqual(definition.Sources(), old.sources) || !reflect.DeepEqual(definition.Dependencies(), old.dependencies) {
		t.Fatal("hidden layout refresh changed visible definition or dependency identity")
	}
	relation := e.Catalog.State.MustRelation(source.Ref.Address)
	for slot, attribute := range definition.Sources()[source.Occurrence].Attributes {
		if live := relation.IndexForAttribute(attribute); live >= 0 {
			if source.Schema()[slot].Usage != relation.Columns[live].Usage {
				t.Fatal("hidden source is not current canonical usage", slot)
			}
		} else if source.Schema()[slot].Usage != old.before.Schema()[slot].Usage {
			t.Fatal("refresh changed unrelated dropped source slot", slot)
		}
	}
}

func TestAlterTypeUnreferencedViewSourceUsage(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT,b INT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1,7)")
	enginetest.Run(t, e, "CREATE VIEW v AS SELECT b FROM t")
	old := captureHiddenView(t, e, "v")
	enginetest.Run(t, e, "ALTER TABLE t ALTER a TYPE TEXT USING a")
	old.assertRefresh(t, e)
}

func TestAlterTypeQuotedViewIdentityCollationAndCascade(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE SCHEMA s")
	enginetest.Run(t, e, "CREATE TABLE t(c VARCHAR(5),discard INT,keep INT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES('seven',1,9)")
	enginetest.Run(t, e, `CREATE VIEW s."Odd View" AS SELECT c,discard FROM public.t`)
	enginetest.Run(t, e, "CREATE VIEW v_keep AS SELECT keep FROM t")
	survivor := captureHiddenView(t, e, "v_keep")
	ref := enginetest.RoleTableRef(t, e, "t")
	old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
	for _, action := range []string{"TYPE VARCHAR(5)", `TYPE VARCHAR(5) COLLATE "C"`, `TYPE VARCHAR(5) COLLATE "default"`} {
		_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "ALTER TABLE public.t ALTER c "+action), nil)
		if err == nil || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || e.Catalog.State.Get(survivor.owner.Address()) != survivor.owner {
			t.Fatal("rejected read-type change published owner state", err)
		}
	}
	enginetest.Run(t, e, "ALTER TABLE t ALTER c TYPE TEXT, DROP discard CASCADE")
	survivor.assertRefresh(t, e)
	current := e.Catalog.State.MustRelation(ref.Address)
	if len(current.Columns) != 2 || current.Columns[0].Usage.Type.ID() != sqltype.Text || current.Columns[0].Usage.Collation.ID != 100 || current.Attributes[1] != 3 {
		t.Fatal("type/drop changed current column usage or stable attribute", current.Columns, current.Attributes)
	}
}
