package enginetests_test

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

type unreadViewFixture struct {
	e              *core.Engine
	read           catalog.Read
	owner, source  *catalog.Relation
	original       *stored.View
	old, target    *sqltype.Descriptor
	stateAttribute int32
}

func unreadViewSetup(t *testing.T, query string) unreadViewFixture {
	t.Helper()
	e := engine.New()
	for _, sql := range []string{
		"CREATE TYPE uv_old AS ENUM ('active')",
		"CREATE TYPE uv_new AS ENUM ('active','dormant')",
		"CREATE TABLE uv_t(id int PRIMARY KEY,state uv_old,payload text)",
		"CREATE TABLE uv_target(id int,state uv_new)",
		"CREATE VIEW uv_unaffected AS SELECT 42 AS answer",
		"CREATE VIEW uv_v AS " + query,
	} {
		enginetest.Run(t, e, sql)
	}
	source := enginetest.PrimaryKeyUnitRelation(t, e, "uv_t")
	owner := enginetest.PrimaryKeyUnitRelation(t, e, "uv_v")
	target := enginetest.PrimaryKeyUnitRelation(t, e, "uv_target")
	return unreadViewFixture{e: e, read: e.CatalogRead(), owner: owner, source: source, original: stored.ViewOf(owner), old: source.Columns[1].Usage.Type, target: target.Columns[1].Usage.Type, stateAttribute: 2}
}

func (f unreadViewFixture) candidate(definition *stored.View) (catalog.Read, *catalog.State) {
	state := catalog.CloneState(f.read.State())
	source := *f.source
	source.Columns = slices.Clone(source.Columns)
	source.Columns[source.IndexForAttribute(f.stateAttribute)].Usage.Type = f.target
	state.SetDescriptor(source.Address(), &source)
	owner := *f.owner
	owner.View = stored.ViewPayload(definition)
	state.SetDescriptor(owner.Address(), &owner)
	return f.read.WithState(state), state
}

func unreadViewNodes(root plan.Node) []plan.Node {
	var nodes []plan.Node
	plan.VisitCTEQuery(root, func(node plan.Node) { nodes = append(nodes, node) }, func(expr.Expr) {}, true)
	return nodes
}

func unreadViewAssertRefresh(t *testing.T, f unreadViewFixture, current catalog.Read, definition *stored.View) {
	t.Helper()
	before, after := unreadViewNodes(f.original.Query().Root), unreadViewNodes(definition.Query().Root)
	if len(before) != len(after) {
		t.Fatal("node layout changed", len(before), len(after))
	}
	refreshed := 0
	for i, old := range before {
		if reflect.TypeOf(old) != reflect.TypeOf(after[i]) || len(old.Schema()) != len(after[i].Schema()) {
			t.Fatal("node kind or frozen width changed")
		}
		for slot, column := range old.Schema() {
			want := column
			if column.Usage.Type == f.old {
				want.Usage.Type = f.target
				refreshed++
			}
			if after[i].Schema()[slot] != want {
				t.Fatalf("%T slot %d lost exact carrier metadata: got %+v want %+v", old, slot, after[i].Schema()[slot], want)
			}
		}
		switch n := old.(type) {
		case *plan.SourceNode:
			mapped := after[i].(*plan.SourceNode)
			if n.Ref != mapped.Ref || n.Occurrence != mapped.Occurrence || n.PresenceSlot != mapped.PresenceSlot {
				t.Fatal("source identity or presence lifetime changed")
			}
		case *plan.JoinNode:
			mapped := after[i].(*plan.JoinNode)
			if n.LeftWidth != mapped.LeftWidth || n.RightWidth != mapped.RightWidth || n.Frame != mapped.Frame || n.CorrelationPrefix != mapped.CorrelationPrefix || n.PresenceSlot != mapped.PresenceSlot || !reflect.DeepEqual(n.Captures, mapped.Captures) {
				t.Fatal("join layout/correlation lifetime changed")
			}
		case *plan.DerivedNode:
			mapped := after[i].(*plan.DerivedNode)
			if n.ScanFrame != mapped.ScanFrame || n.PresenceSlot != mapped.PresenceSlot || !reflect.DeepEqual(n.Captures, mapped.Captures) {
				t.Fatal("derived capture/presence lifetime changed")
			}
		case *plan.AggregateNode:
			mapped := after[i].(*plan.AggregateNode)
			if !reflect.DeepEqual(n.Dependencies, mapped.Dependencies) || len(n.Dependent) != len(mapped.Dependent) {
				t.Fatal("group proof/payload layout changed")
			}
			for j := range n.Dependent {
				if n.Dependent[j].ResultUsage() != mapped.Dependent[j].ResultUsage() {
					t.Fatal("expression-owned group payload overridden")
				}
			}
		}
	}
	if refreshed == 0 {
		t.Fatal("fixture did not retain an unread source carrier")
	}
	if !reflect.DeepEqual(f.original.Parts().Sources, definition.Parts().Sources) || !reflect.DeepEqual(f.original.Dependencies(), definition.Dependencies()) {
		t.Fatal("source occurrence/attribute/read or dependency authority changed")
	}
	if err := definition.Validate(current, current.MustRelation(f.owner.Address())); err != nil {
		t.Fatal("refreshed definition rejected", err)
	}
}

func TestUnreadViewSourceRefreshAuthority(t *testing.T) {
	f := unreadViewSetup(t, "SELECT id FROM uv_t WHERE id > 0 ORDER BY id LIMIT 2")
	current, state := f.candidate(f.original)
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	if !stored.ValidExpressionUsage(current, f.source.Columns[1].Usage) || stored.ExpressionDescriptorsChanged(current, f.owner, identity) {
		t.Fatal("fixture did not isolate the canonical cross-ID detector omission")
	}
	unaffected := enginetest.PrimaryKeyUnitRelation(t, f.e, "uv_unaffected")
	if err := stored.ReownExpressions(current, state, nil, true, nil); err != nil {
		t.Fatal(err)
	}
	mapped := stored.ViewOf(current.MustRelation(f.owner.Address()))
	unreadViewAssertRefresh(t, f, current, mapped)
	if current.Get(unaffected.Address()) != unaffected || mapped.Query().Root.Schema()[0].Usage != f.original.Query().Root.Schema()[0].Usage {
		t.Fatal("unaffected stored payload or visible INT4 output replaced")
	}
	for _, node := range unreadViewNodes(mapped.Query().Root) {
		plan.QueryExpressions(node, func(bound expr.Expr) {
			plan.WalkExpression(bound, func(bound expr.Expr) {
				if column, ok := bound.(plan.ColumnExpr); ok && column.ResultUsage().Type != current.MustRelation(f.source.Address()).Columns[0].Usage.Type {
					t.Fatal("read expression usage overridden")
				}
			})
		})
	}
	t.Run("same_identity_generation", func(t *testing.T) {
		f := unreadViewSetup(t, "SELECT id FROM uv_t")
		enginetest.Run(t, f.e, "ALTER TYPE uv_old ADD VALUE 'dormant'")
		read := f.e.CatalogRead()
		live := read.MustRelation(f.source.Address()).Columns[1].Usage.Type
		if live == f.old || live.ID() != f.old.ID() {
			t.Fatal("fixture did not produce a genuine same-ID enum successor")
		}
		state := catalog.CloneState(read.State())
		owner := *read.MustRelation(f.owner.Address())
		owner.View = stored.ViewPayload(f.original)
		state.SetDescriptor(owner.Address(), &owner)
		read = read.WithState(state)
		if err := stored.ReownExpressions(read, state, nil, true, nil); err != nil {
			t.Fatal("ordinary generation remap rejected", err)
		}
		for _, node := range unreadViewNodes(stored.ViewOf(read.MustRelation(owner.Address())).Query().Root) {
			for _, column := range node.Schema() {
				if column.Usage.Type == f.old {
					t.Fatal("same-ID carrier retained the predecessor")
				}
			}
		}
	})
	t.Run("stable_attribute_after_compaction", func(t *testing.T) {
		e := engine.New()
		for _, sql := range []string{
			"CREATE TYPE uv_old AS ENUM ('active')",
			"CREATE TYPE uv_new AS ENUM ('active','dormant')",
			"CREATE TABLE uv_t(pad text,id int,state uv_old)",
			"CREATE TABLE uv_target(state uv_new)",
			"CREATE VIEW uv_v AS SELECT id FROM uv_t",
		} {
			enginetest.Run(t, e, sql)
		}
		owner := enginetest.PrimaryKeyUnitRelation(t, e, "uv_v")
		original := stored.ViewOf(owner)
		source := enginetest.PrimaryKeyUnitRelation(t, e, "uv_t")
		old := source.Columns[2].Usage.Type
		enginetest.Run(t, e, "ALTER TABLE uv_t DROP COLUMN pad")
		enginetest.Run(t, e, "ALTER TABLE uv_t RENAME COLUMN state TO renamed_state")
		source = enginetest.PrimaryKeyUnitRelation(t, e, "uv_t")
		target := enginetest.PrimaryKeyUnitRelation(t, e, "uv_target")
		f := unreadViewFixture{e: e, read: e.CatalogRead(), owner: owner, source: source, original: original, old: old, target: target.Columns[0].Usage.Type, stateAttribute: 3}
		current, state := f.candidate(original)
		if source.IndexForAttribute(3) != 1 || len(original.Parts().Sources[0].Attributes) != 3 {
			t.Fatal("fixture lost stable-ID/dense-ordinal distinction")
		}
		if err := stored.ReownExpressions(current, state, nil, true, nil); err != nil {
			t.Fatal(err)
		}
		unreadViewAssertRefresh(t, f, current, stored.ViewOf(current.MustRelation(owner.Address())))
	})
	t.Run("frozen_star_width_after_add", func(t *testing.T) {
		f := unreadViewSetup(t, "SELECT d.* FROM (SELECT id FROM uv_t) d")
		enginetest.Run(t, f.e, "ALTER TABLE uv_t ADD COLUMN later int")
		f.read = f.e.CatalogRead()
		f.source = f.read.MustRelation(f.source.Address())
		current, state := f.candidate(f.original)
		if len(f.source.Attributes) != 4 || len(f.original.Parts().Sources[0].Attributes) != 3 {
			t.Fatal("fixture lost creation/current width distinction")
		}
		if err := stored.ReownExpressions(current, state, nil, true, nil); err != nil {
			t.Fatal(err)
		}
		mapped := stored.ViewOf(current.MustRelation(f.owner.Address()))
		unreadViewAssertRefresh(t, f, current, mapped)
		if len(mapped.Query().Root.Schema()) != 1 {
			t.Fatal("source refresh expanded a creation-time derived star")
		}
	})
}

func TestUnreadViewSourceRefreshCarriers(t *testing.T) {
	for _, test := range []struct{ name, query string }{
		{"source", "SELECT id FROM uv_t"},
		{"passthrough", "SELECT DISTINCT id FROM uv_t WHERE id > 0 ORDER BY id LIMIT 2 OFFSET 1"},
		{"distinct_projected", "SELECT DISTINCT payload,id,id+1 FROM uv_t ORDER BY id"},
		{"distinct_on", "SELECT DISTINCT ON (id) id FROM uv_t ORDER BY id"},
		{"join", "SELECT a.id FROM uv_t a LEFT JOIN uv_t b ON a.id=b.id"},
		{"natural", "SELECT a.id FROM uv_t a NATURAL JOIN (SELECT id FROM uv_t) b"},
		{"evaluation", "SELECT id+1 FROM uv_t LIMIT 2"},
		{"window", "SELECT id,row_number() OVER (ORDER BY id) FROM uv_t"},
		{"set_project", "SELECT id,string_to_table(payload,',') FROM uv_t"},
		{"project_derived", "SELECT d.id FROM (SELECT id FROM uv_t) d"},
		{"cte_copies", "WITH q AS (SELECT id FROM uv_t) SELECT a.id FROM q a JOIN q b ON a.id=b.id"},
		{"cte_unused", "WITH q AS (SELECT id FROM uv_t) SELECT 1 AS id"},
		{"recursive", "WITH RECURSIVE q(id) AS (SELECT id FROM uv_t UNION ALL SELECT id+1 FROM q WHERE id<2) SELECT id FROM q"},
		{"group", "SELECT id,count(*) FROM uv_t GROUP BY id"},
		{"group_dependent", "SELECT id,payload,count(*) FROM uv_t GROUP BY id"},
		{"group_system", "SELECT id,count(*) FROM uv_t GROUP BY id HAVING tableoid IS NOT NULL ORDER BY tableoid"},
		{"lateral", "SELECT a.id FROM uv_t a LEFT JOIN LATERAL (SELECT b.id FROM uv_t b WHERE b.id=a.id LIMIT 1) b ON true"},
		{"set", "SELECT id FROM uv_t UNION ALL SELECT id FROM uv_t"},
		{"row_lock", "SELECT id FROM uv_t FOR UPDATE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := unreadViewSetup(t, test.query)
			var expected plan.Node
			switch test.name {
			case "passthrough", "distinct_projected":
				expected = (*plan.DistinctNode)(nil)
			case "distinct_on":
				expected = (*plan.DistinctOnNode)(nil)
			case "evaluation":
				expected = (*plan.EvaluationNode)(nil)
			case "window":
				expected = (*plan.WindowNode)(nil)
			case "set_project":
				expected = (*plan.SetProjectNode)(nil)
			case "row_lock":
				expected = (*plan.RowLockNode)(nil)
			case "recursive":
				expected = (*plan.RecursiveUnionNode)(nil)
			case "group", "group_dependent", "group_system":
				expected = (*plan.AggregateNode)(nil)
			case "project_derived", "cte_copies", "lateral":
				expected = (*plan.DerivedNode)(nil)
			}
			if expected != nil {
				found := false
				for _, node := range unreadViewNodes(f.original.Query().Root) {
					found = found || reflect.TypeOf(node) == reflect.TypeOf(expected)
				}
				if !found {
					t.Fatalf("fixture omitted admitted layout %T", expected)
				}
			}
			if test.name == "passthrough" || test.name == "distinct_projected" {
				for _, node := range unreadViewNodes(f.original.Query().Root) {
					distinct, ok := node.(*plan.DistinctNode)
					if !ok {
						continue
					}
					if len(distinct.Schema()) != len(distinct.Expressions) || distinct.Child.Schema()[1].Usage.Type != f.old {
						t.Fatal("fixture lost DISTINCT projection/unread input split")
					}
					if test.name == "passthrough" && len(distinct.Schema()) >= len(distinct.Child.Schema()) {
						t.Fatal("fixture lost narrower DISTINCT output")
					}
					if test.name == "distinct_projected" && len(distinct.Schema()) != len(distinct.Child.Schema()) {
						t.Fatal("fixture lost equal-width nonidentity DISTINCT output")
					}
					for slot, column := range distinct.Schema() {
						if column.Usage != distinct.Expressions[slot].ResultUsage() || column.Usage.Type == f.old {
							t.Fatal("DISTINCT output is not owned by its projected expression")
						}
					}
				}
			}
			current, state := f.candidate(f.original)
			if err := stored.ReownExpressions(current, state, nil, true, nil); err != nil {
				t.Fatal(err)
			}
			unreadViewAssertRefresh(t, f, current, stored.ViewOf(current.MustRelation(f.owner.Address())))
			// Each actual copied-parent layout must authenticate its original
			// Usage, not merely overwrite a corrupt carrier with the child.
			for _, originalNode := range unreadViewNodes(f.original.Query().Root) {
				slot := -1
				switch originalNode.(type) {
				case *plan.FilterNode, *plan.SortNode, *plan.DistinctNode, *plan.DistinctOnNode, *plan.LimitNode, *plan.RowLockNode, *plan.EvaluationNode, *plan.WindowNode, *plan.SetProjectNode, *plan.JoinNode:
					for i, column := range originalNode.Schema() {
						if column.Usage.Type == f.old {
							slot = i
							break
						}
					}
				}
				if slot < 0 {
					continue
				}
				bad := unreadViewEdit(t, f.original, func(node plan.Node) plan.Node {
					if node != originalNode {
						return node
					}
					schema := slices.Clone(node.Schema())
					foreign := *f.old
					schema[slot].Usage.Type = &foreign
					return unreadViewWithSchema(node, schema)
				}, nil)
				badRead, badState := f.candidate(bad)
				if err := stored.ReownExpressions(badRead, badState, nil, true, nil); err == nil {
					t.Fatalf("copied %T carrier accepted a foreign original", originalNode)
				}
			}
		})
	}
	t.Run("copied_parent_must_match", func(t *testing.T) {
		f := unreadViewSetup(t, "SELECT id FROM uv_t WHERE id > 0")
		bad := unreadViewEdit(t, f.original, func(node plan.Node) plan.Node {
			if n, ok := node.(*plan.FilterNode); ok {
				copy := *n
				copy.QuerySchema = slices.Clone(n.QuerySchema)
				foreign := *f.old
				copy.QuerySchema[1].Usage.Type = &foreign
				return &copy
			}
			return node
		}, nil)
		current, state := f.candidate(bad)
		if err := stored.ReownExpressions(current, state, nil, true, nil); err == nil {
			t.Fatal("forged copied parent carrier laundered")
		}
	})
}

func unreadViewEdit(t *testing.T, original *stored.View, edit func(plan.Node) plan.Node, partsEdit func(*stored.ViewParts)) *stored.View {
	t.Helper()
	root, err := plan.RewriteQueryExpressions(original.Query().Root, func(bound expr.Expr) (expr.Expr, bool, error) { return bound, false, nil }, func(node plan.Node) (plan.Node, error) { return edit(node), nil })
	if err != nil {
		t.Fatal(err)
	}
	return original.With(func(parts *stored.ViewParts) {
		query := *parts.Query
		query.Root = root
		parts.Query = &query
		parts.Frames, parts.Queries = analyze.CollectQueryFrames(root)
		parts.Sources = slices.Clone(parts.Sources)
		for i := range parts.Sources {
			parts.Sources[i].Attributes = slices.Clone(parts.Sources[i].Attributes)
			parts.Sources[i].Reads = slices.Clone(parts.Sources[i].Reads)
		}
		if partsEdit != nil {
			partsEdit(parts)
		}
	})
}

func TestUnreadViewSourceRefreshImmutable(t *testing.T) {
	f := unreadViewSetup(t, "SELECT a.id FROM uv_t a LEFT JOIN uv_t b ON a.id=b.id WHERE a.id > 0 ORDER BY a.id")
	nodes := unreadViewNodes(f.original.Query().Root)
	schemas := make([]plan.QuerySchema, len(nodes))
	for i, node := range nodes {
		schemas[i] = slices.Clone(node.Schema())
	}
	parts := f.original.Parts()
	leaves := slices.Clone(parts.Sources)
	for i := range leaves {
		leaves[i].Attributes = slices.Clone(leaves[i].Attributes)
		leaves[i].Reads = slices.Clone(leaves[i].Reads)
	}
	dependencies := slices.Clone(parts.Dependencies)
	output := slices.Clone(parts.Query.Root.Schema())
	members := f.old.EnumMembers()
	current, state := f.candidate(f.original)
	if err := stored.ReownExpressions(current, state, nil, true, nil); err != nil {
		t.Fatal(err)
	}
	accepted := stored.ViewOf(current.MustRelation(f.owner.Address()))
	unreadViewAssertRefresh(t, f, current, accepted)
	acceptedNodes := unreadViewNodes(accepted.Query().Root)
	acceptedSchemas := make([]plan.QuerySchema, len(acceptedNodes))
	for i, node := range acceptedNodes {
		acceptedSchemas[i] = slices.Clone(node.Schema())
		if len(node.Schema()) != 0 && &node.Schema()[0] == &nodes[i].Schema()[0] {
			t.Fatal("changed graph shares writable predecessor schema backing")
		}
	}
	// A fresh operation must not inherit either accepted project memo entries
	// or the private source-layout policy. Standalone remapping has no repair
	// permission and therefore retains the original old-enum hidden carriers.
	mapper := stored.ViewQueryRemapper{Catalog: current, Ref: func(ref ids.ObjectRef) ids.ObjectRef { return ref }, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }}
	first, err := mapper.Nested(accepted.Query().Root)
	if err != nil {
		t.Fatal(err)
	}
	again, err := mapper.Nested(accepted.Query().Root)
	if err != nil || first != again {
		t.Fatal("operation lost its accepted canonical project", err)
	}
	fresh := mapper.Fresh()
	fresh.Type = func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == f.target {
			return f.old
		}
		return d
	}
	second, err := fresh.Nested(accepted.Query().Root)
	if err != nil || second == first {
		t.Fatal("Fresh inherited an accepted project", err)
	}
	var secondSource *plan.SourceNode
	for _, node := range unreadViewNodes(second) {
		if source, ok := node.(*plan.SourceNode); ok {
			secondSource = source
			if source.Schema()[1].Usage.Type != f.old {
				t.Fatal("Fresh inherited previous destination types")
			}
		}
	}
	if secondSource == nil {
		t.Fatal("missing fresh source")
	}
	bad := unreadViewEdit(t, f.original, func(node plan.Node) plan.Node {
		if n, ok := node.(*plan.SourceNode); ok {
			copy := *n
			copy.QuerySchema = slices.Clone(n.QuerySchema)
			foreign := *f.old
			copy.QuerySchema[1].Usage.Type = &foreign
			return &copy
		}
		return node
	}, nil)
	brokenRead, broken := f.candidate(bad)
	if err := stored.ReownExpressions(brokenRead, broken, nil, true, nil); err == nil {
		t.Fatal("forged later operation accepted")
	}
	for i, node := range nodes {
		if !slices.Equal(node.Schema(), schemas[i]) {
			t.Fatal("original schema backing changed")
		}
	}
	for i, node := range acceptedNodes {
		if !slices.Equal(node.Schema(), acceptedSchemas[i]) {
			t.Fatal("retained accepted graph changed after Fresh/failure")
		}
	}
	if parts.Query != f.original.Query() || !reflect.DeepEqual(parts.Sources, leaves) || !reflect.DeepEqual(parts.Dependencies, dependencies) || !slices.Equal(parts.Query.Root.Schema(), output) || !slices.Equal(f.old.EnumMembers(), members) {
		t.Fatal("predecessor query/leaf/dependency/output/enum backing changed")
	}
	if f.read.MustRelation(f.owner.Address()) != f.owner || stored.ViewOf(f.owner) != f.original {
		t.Fatal("candidate operation published into the retained catalog")
	}
}

func TestUnreadViewSourceRefreshRejections(t *testing.T) {
	for _, name := range []string{
		"copied_descriptor", "foreign_same_oid", "foreign_catalog", "retired_generation", "retired_parent",
		"foreign_domain", "zero_epoch", "future_epoch", "stale_epoch",
		"source_address", "source_ref", "subid", "wrong_occurrence",
		"repeated_occurrence", "swapped_occurrences", "fabricated_attribute", "missing_attribute", "missing_origin",
		"read", "whole_row", "system", "copied_parent", "foreign_target",
	} {
		t.Run(name, func(t *testing.T) {
			query := "SELECT id FROM uv_t WHERE id > 0"
			if name == "repeated_occurrence" || name == "swapped_occurrences" {
				query = "SELECT a.id FROM uv_t a JOIN uv_t b ON a.id=b.id"
			}
			f := unreadViewSetup(t, query)
			retired := f.old
			var foreignCatalog *sqltype.Descriptor
			if name == "foreign_catalog" {
				foreign := unreadViewSetup(t, query)
				foreignCatalog = foreign.old
				if foreignCatalog == f.old {
					t.Fatal("foreign fixture shared the original catalog descriptor")
				}
			}
			if name == "retired_generation" || name == "retired_parent" {
				enginetest.Run(t, f.e, "ALTER TYPE uv_old ADD VALUE 'another'")
				f.read = f.e.CatalogRead()
				if name == "retired_parent" {
					f.owner = f.read.MustRelation(f.owner.Address())
					f.source = f.read.MustRelation(f.source.Address())
					f.original = stored.ViewOf(f.owner)
					f.old = f.source.Columns[1].Usage.Type
				}
			}
			changedRef := func(old ids.ObjectRef) ids.ObjectRef {
				switch name {
				case "foreign_domain":
					return new(ids.Domain).Ref(old.Epoch, old.Address)
				case "zero_epoch":
					return old.Domain().Ref(0, old.Address)
				case "future_epoch":
					return old.Domain().Ref(f.read.Epoch()+1, old.Address)
				case "source_address":
					old.Address.ObjectID = 99999999
				case "subid":
					old.Address.SubID = 2
				}
				return old
			}
			bad := unreadViewEdit(t, f.original, func(node plan.Node) plan.Node {
				if n, ok := node.(*plan.SourceNode); ok {
					copy := *n
					copy.QuerySchema = slices.Clone(n.QuerySchema)
					copy.Ref = changedRef(n.Ref)
					switch name {
					case "copied_descriptor":
						foreign := *f.old
						copy.QuerySchema[1].Usage.Type = &foreign
					case "foreign_same_oid":
						foreign, err := sqltype.Enum(f.old.ID(), "uv_old", "uv_old", []string{"active"})
						if err != nil {
							t.Fatal(err)
						}
						copy.QuerySchema[1].Usage.Type = foreign
					case "foreign_catalog":
						copy.QuerySchema[1].Usage.Type = foreignCatalog
					case "missing_origin":
						copy.QuerySchema[1].OriginAttribute = 0
					case "source_ref":
						copy.Ref.Address.ObjectID++
					case "wrong_occurrence":
						copy.Occurrence = 999
					case "repeated_occurrence":
						copy.Occurrence = 0
					case "swapped_occurrences":
						copy.Occurrence = 1 - n.Occurrence
					}
					return &copy
				}
				if n, ok := node.(*plan.FilterNode); ok && (name == "copied_parent" || name == "retired_parent") {
					copy := *n
					copy.QuerySchema = slices.Clone(n.QuerySchema)
					foreign := *f.old
					copy.QuerySchema[1].Usage.Type = &foreign
					if name == "retired_parent" {
						copy.QuerySchema[1].Usage.Type = retired
					}
					return &copy
				}
				return node
			}, func(parts *stored.ViewParts) {
				for i := range parts.Sources {
					parts.Sources[i].Ref = changedRef(parts.Sources[i].Ref)
				}
				switch name {
				case "fabricated_attribute":
					parts.Sources[0].Attributes[1] = 1
				case "missing_attribute":
					parts.Sources[0].Attributes[1] = 999
				case "read":
					parts.Sources[0].Reads = append(parts.Sources[0].Reads, 2)
				case "whole_row":
					parts.Sources[0].WholeRowRead = true
				case "system":
					parts.Sources[0].SystemAttributeRead = true
					parts.Sources[0].SystemColumns = []int32{2}
				}
			})
			current, state := f.candidate(bad)
			if name == "stale_epoch" {
				state.SetEpoch(current.Epoch() + 1)
			}
			if name == "foreign_target" {
				target := *state.MustRelation(f.source.Address())
				target.Columns = slices.Clone(target.Columns)
				foreign := *f.target
				target.Columns[1].Usage.Type = &foreign
				state.SetDescriptor(target.Address(), &target)
			}
			before := unreadViewNodes(bad.Query().Root)
			schemas := make([]plan.QuerySchema, len(before))
			for i, node := range before {
				schemas[i] = slices.Clone(node.Schema())
			}
			if err := stored.ReownExpressions(current, state, nil, true, nil); err == nil {
				t.Fatal("forged or ineligible source accepted")
			}
			for i, node := range before {
				if !slices.Equal(node.Schema(), schemas[i]) {
					t.Fatal("rejected original backing changed")
				}
			}
			if stored.ViewOf(state.MustRelation(f.owner.Address())) != bad || stored.ViewOf(f.read.MustRelation(f.owner.Address())) == bad {
				t.Fatal("failure attached a replacement or published the candidate")
			}
		})
	}
	for _, query := range []string{
		"SELECT state FROM uv_t",
		"SELECT state::text FROM uv_t",
		"SELECT id FROM uv_t WHERE state::text='active'",
		"SELECT id FROM uv_t ORDER BY state",
		"SELECT a.id FROM uv_t a JOIN uv_t b ON a.state=b.state",
	} {
		t.Run(query, func(t *testing.T) {
			f := unreadViewSetup(t, query)
			current, state := f.candidate(f.original)
			if err := stored.ReownExpressions(current, state, nil, true, nil); err == nil {
				t.Fatal("genuinely read/expression-owned source usage refreshed")
			}
		})
	}
	t.Run("independent_cast_stays_owned", func(t *testing.T) {
		f := unreadViewSetup(t, "SELECT id,'active'::uv_old AS retained FROM uv_t")
		current, state := f.candidate(f.original)
		before := slices.Clone(f.original.Query().Root.Schema())
		dependencies := slices.Clone(f.original.Dependencies())
		if err := stored.ReownExpressions(current, state, nil, true, nil); err != nil {
			t.Fatal(err)
		}
		mapped := stored.ViewOf(current.MustRelation(f.owner.Address()))
		if !slices.Equal(mapped.Query().Root.Schema(), before) || mapped.Query().Root.Schema()[1].Usage.Type != f.old || !reflect.DeepEqual(mapped.Dependencies(), dependencies) {
			t.Fatal("source refresh overrode explicit cast/output/type dependency")
		}
		if err := mapped.Validate(current, current.MustRelation(f.owner.Address())); err != nil {
			t.Fatal(err)
		}
	})
}

func unreadViewWithSchema(node plan.Node, schema plan.QuerySchema) plan.Node {
	switch n := node.(type) {
	case *plan.FilterNode:
		copy := *n
		copy.QuerySchema = schema
		return &copy
	case *plan.SortNode:
		copy := *n
		copy.QuerySchema = schema
		return &copy
	case *plan.DistinctNode:
		copy := *n
		copy.QuerySchema = schema
		return &copy
	case *plan.DistinctOnNode:
		copy := *n
		copy.QuerySchema = schema
		return &copy
	case *plan.LimitNode:
		copy := *n
		copy.QuerySchema = schema
		return &copy
	case *plan.RowLockNode:
		copy := *n
		copy.QuerySchema = schema
		return &copy
	case *plan.EvaluationNode:
		copy := *n
		copy.QuerySchema = schema
		return &copy
	case *plan.WindowNode:
		copy := *n
		copy.QuerySchema = schema
		return &copy
	case *plan.SetProjectNode:
		copy := *n
		copy.QuerySchema = schema
		return &copy
	case *plan.JoinNode:
		copy := *n
		copy.QuerySchema = schema
		return &copy
	default:
		panic("not a copied source-layout carrier")
	}
}
