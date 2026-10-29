package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func replicaUnitFixture(t *testing.T) (*core.Engine, *session.Session, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	s := sessionStart(t, e, nil)
	name := preparedBoundaryName("ri_unit")
	preparedUnitStatement(t, e, s, &parser.CreateTable{Name: name, Elements: []parser.TableElement{
		{Column: &parser.Column{Name: "k", TypeName: parser.TypeName{Name: "int4"}, Constraints: []parser.ColumnConstraint{{NotNull: true}}}},
		{Column: &parser.Column{Name: "p", TypeName: parser.TypeName{Name: "text"}}},
	}})
	for _, n := range []string{"ri_a", "ri_b", "ri_c"} {
		preparedUnitStatement(t, e, s, &parser.CreateIndex{Name: parser.Identifier{Text: n}, Table: name, Method: "btree", Unique: true, Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "k"}}}, Include: []parser.IndexKey{{Column: parser.Identifier{Text: "p"}}}})
	}
	preparedUnitStatement(t, e, s, &parser.Insert{Name: name, Rows: [][]parser.Expr{{preparedBoundaryInteger(1), parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "one"}}}}})
	return e, s, e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "ri_unit").Address)
}
func replicaUnitAlter(mode parser.ReplicaIdentityMode, index string) *parser.AlterTable {
	return &parser.AlterTable{Name: preparedBoundaryName("ri_unit"), Actions: []parser.AlterTableAction{{Action: parser.SetReplicaIdentity, ReplicaIdentity: mode, ReplicaIndex: parser.Identifier{Text: index}}}}
}
func replicaUnitIndex(t *testing.T, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	a, ok := e.CatalogRead().LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	if !ok {
		t.Fatal("missing index", name)
	}
	return e.CatalogRead().MustRelation(a)
}

func TestReplicaIdentityCandidate(t *testing.T) {
	e, s, old := replicaUnitFixture(t)
	a, b, c := replicaUnitIndex(t, e, "ri_a"), replicaUnitIndex(t, e, "ri_b"), replicaUnitIndex(t, e, "ri_c")
	preparedUnitStatement(t, e, s, replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_a"))
	first := e.CatalogRead().MustRelation(old.Address())
	firstA := replicaUnitIndex(t, e, "ri_a")
	if old.ReplicaIdentity != 0 || a.SQLIndex.ReplicaIdentity || !firstA.SQLIndex.ReplicaIdentity || first.EffectiveReplicaIdentity() != parser.ReplicaIdentityIndex {
		t.Fatal("selection mutated predecessor or omitted current bit")
	}
	preparedUnitStatement(t, e, s, replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_b"))
	current := e.CatalogRead().MustRelation(old.Address())
	currentA, currentB := replicaUnitIndex(t, e, "ri_a"), replicaUnitIndex(t, e, "ri_b")
	if current.Revision() <= first.Revision() || currentA.SQLIndex.ReplicaIdentity || !currentB.SQLIndex.ReplicaIdentity || !firstA.SQLIndex.ReplicaIdentity || b.SQLIndex.ReplicaIdentity {
		t.Fatal("same-mode switch lost immutable revisions")
	}
	if &currentA.SQLIndex.Keys[0] != &a.SQLIndex.Keys[0] || &currentB.SQLIndex.Include[0] != &b.SQLIndex.Include[0] || replicaUnitIndex(t, e, "ri_c") != c {
		t.Fatal("selection copied keys or replaced unrelated descriptor")
	}
	preparedUnitStatement(t, e, s, replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_b"))
	for _, mode := range []parser.ReplicaIdentityMode{parser.ReplicaIdentityFull, parser.ReplicaIdentityNothing, parser.ReplicaIdentityDefault} {
		preparedUnitStatement(t, e, s, replicaUnitAlter(mode, ""))
		if e.CatalogRead().MustRelation(old.Address()).EffectiveReplicaIdentity() != mode || replicaUnitIndex(t, e, "ri_b").SQLIndex.ReplicaIdentity {
			t.Fatal("mode did not clear selection")
		}
	}
	// MISC must update an index created in this statement's ADD pass without
	// inventing a base predecessor or replacing the stage's creation fences.
	alter := replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_new")
	alter.Actions = append([]parser.AlterTableAction{{
		Action: parser.AddUnique,
		Unique: &parser.UniqueConstraint{
			Name:    parser.Identifier{Text: "ri_new"},
			Columns: []parser.Identifier{{Text: "k"}},
		},
	}}, alter.Actions...)
	preparedUnitStatement(t, e, s, alter)
	parent := e.CatalogRead().MustRelation(old.Address())
	created := replicaUnitIndex(t, e, "ri_new")
	if parent.EffectiveReplicaIdentity() != parser.ReplicaIdentityIndex || !created.SQLIndex.ReplicaIdentity || created.SQLIndex.Parent != parent.Address() || len(parent.Constraints.Unique) != 1 || parent.Constraints.Unique[0].Index != created.Address() {
		t.Fatal("ADD/MISC lost parent, backing constraint or index selection")
	}
	for _, name := range []string{"ri_a", "ri_b", "ri_c"} {
		if replicaUnitIndex(t, e, name).SQLIndex.ReplicaIdentity {
			t.Fatal("ADD/MISC retained another selected index", name)
		}
	}
	alter = replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_new")
	alter.Actions = append([]parser.AlterTableAction{{
		Action:   parser.AlterColumnType,
		Column:   parser.Identifier{Text: "k"},
		TypeName: parser.TypeName{Name: "int8"},
	}}, alter.Actions...)
	preparedUnitStatement(t, e, s, alter)
	reminted := replicaUnitIndex(t, e, "ri_new")
	if reminted.Address() == created.Address() || !reminted.SQLIndex.ReplicaIdentity || reminted.Columns[0].Usage.Type.ID() != sqltype.Int8 || !created.SQLIndex.ReplicaIdentity || created.Columns[0].Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("TYPE/MISC lost reminted selection or mutated its predecessor")
	}
}

func TestReplicaIdentityPublication(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "later_error", true: "cancelled_finish"}[cancelled], func(t *testing.T) {
			e, s, old := replicaUnitFixture(t)
			preparedUnitStatement(t, e, s, replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_a"))
			before := e.CatalogRead().MustRelation(old.Address())
			a, b := replicaUnitIndex(t, e, "ri_a"), replicaUnitIndex(t, e, "ri_b")
			heap := e.Storage.Relations[old.Address()]
			root := heap.Versions
			generation := heap.Generation
			aRoot, bRoot := e.Storage.IndexRoots[a.Address()], e.Storage.IndexRoots[b.Address()]
			c := replicaUnitIndex(t, e, "ri_c")
			cRoot := e.Storage.IndexRoots[c.Address()]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			alter := replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_b")
			if !cancelled {
				alter.Actions = append(alter.Actions, parser.AlterTableAction{Action: parser.SetReplicaIdentity, ReplicaIdentity: parser.ReplicaIdentityIndex, ReplicaIndex: parser.Identifier{Text: "ri_missing"}})
			}
			_, err = scope.Execute(alter, 0, nil)
			if cancelled {
				if err != nil {
					t.Fatal(err)
				}
				if scope.Catalog == nil || scope.Catalog.Descriptors[a.Address()] == nil || scope.Catalog.Descriptors[b.Address()] == nil || e.CatalogRead().MustRelation(old.Address()) != before {
					t.Fatal("complete candidate not privately retained")
				}
				cancel()
			}
			err = scope.Finish(err)
			op.Done()
			if cancelled {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("canceled finish", err)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" {
					t.Fatal(err)
				}
			}
			if e.CatalogRead().MustRelation(old.Address()) != before || replicaUnitIndex(t, e, "ri_a") != a || replicaUnitIndex(t, e, "ri_b") != b || e.Storage.Relations[old.Address()] != heap || heap.Generation != generation || len(heap.Versions) != len(root) {
				t.Fatal("rejected publication replaced catalog/storage authority")
			}
			if e.Storage.IndexRoots[a.Address()] != aRoot || e.Storage.IndexRoots[b.Address()] != bRoot || e.Storage.IndexRoots[c.Address()] != cRoot || &heap.Versions[0] != &root[0] {
				t.Fatal("rejected publication changed immutable root backing")
			}
			preparedUnitStatement(t, e, s, replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_b"))
			if e.Storage.Relations[old.Address()] != heap || heap.Generation != generation {
				t.Fatal("metadata selection rewrote heap")
			}
			if e.Storage.IndexRoots[a.Address()] != aRoot || e.Storage.IndexRoots[b.Address()] != bRoot || e.Storage.IndexRoots[c.Address()] != cRoot {
				t.Fatal("metadata selection rebuilt index roots")
			}
		})
	}
}

func TestReplicaIdentityPreparedInvalidation(t *testing.T) {
	e, s, old := replicaUnitFixture(t)
	preparedUnitStatement(t, e, s, replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_a"))
	// Utility ALTER is deliberately not a retained preparable-body analysis.
	// Use a real retained SELECT owner; public held ALTER coverage separately
	// verifies execution-time index resolution and authorization.
	ast := &parser.Select{
		From: &parser.TableReference{Name: preparedBoundaryName("ri_unit")},
		Targets: []parser.Expr{
			parser.ColumnExpr{Name: preparedBoundaryName("k")},
			// Match the demanded unary ownership fixture in
			// TestPreparedFoldTypedOwnership. Bare arithmetic projections are
			// execution-only; COALESCE demands this immutable operator result.
			parser.ConditionalExpr{Kind: parser.Coalesce, Args: []parser.Expr{
				parser.OperatorExpr{Name: preparedBoundaryName("-"), Args: []parser.Expr{
					parser.CastExpr{Child: preparedBoundaryInteger(7), TypeName: parser.TypeName{Name: "int4"}, Syntax: parser.PostfixCast},
				}},
				preparedBoundaryInteger(0),
			}},
		},
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	description, err := e.Describe(op, ast)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	execute := func() {
		t.Helper()
		result, err := sequencePreparedExecute(e, s, ast, &description)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Rows.Close()
		if rows := enginetest.Tuples(result); !reflect.DeepEqual(rows, [][]driver.Value{{int64(1), int64(-7)}}) {
			t.Fatal("retained SELECT values", rows)
		}
	}
	valid := func(analysis *core.Analysis) bool {
		t.Helper()
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		env, err := e.PreparedEnvironmentLocked(auth, false)
		if err != nil {
			t.Fatal(err)
		}
		return analysis.Valid(auth.Catalog, env)
	}
	execute()
	accepted := description.Analysis
	before := e.CatalogRead().MustRelation(old.Address())
	if accepted == nil || !valid(accepted) {
		t.Fatal("missing valid retained SELECT analysis")
	}
	hasParentStamp := func(analysis *core.Analysis, relation *catalog.Relation) bool {
		for _, dependency := range analysis.Dependencies {
			if dependency.Ref.Address == relation.Address() && dependency.Revision == relation.Revision() {
				return true
			}
		}
		return false
	}
	if !hasParentStamp(accepted, before) || description.Folds == nil {
		t.Fatal("retained SELECT omitted actual parent dependency or fold owner")
	}
	owner := description.Folds
	folds := owner.Current()
	if len(folds.Entries) == 0 || owner.Analysis != accepted {
		t.Fatal("missing accepted immutable fold backing")
	}
	entry := preparedFoldEntry(t, description, sqltype.Int4, "-7")
	// Seed operators dispatch directly to ArithmeticExpr. Their fold carries
	// the real int4um routine, not a SelectedOperatorExpr's operator Ref.
	if entry.Operator != (ids.ObjectRef{}) {
		t.Fatalf("builtin unary fold operator ref: got %+v, want zero provider ref", entry.Operator)
	}
	callable, ok := entry.Callable.(*catalog.Procedure)
	expectedCallable := e.CatalogRead().Get(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 212})
	if !ok || callable == nil || callable != expectedCallable || callable.Name() != "int4um" {
		t.Fatalf("unary callable: got %T %+v, want current pg_catalog.int4um descriptor %+v", entry.Callable, entry.Callable, expectedCallable)
	}
	integer, ok := e.CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing actual INT4 owner")
	}
	if entry.Result != integer.Usage() {
		t.Fatalf("inner unary result usage: got %+v, want %+v; outer conditional usage %+v", entry.Result, integer.Usage(), description.Results[1].Usage)
	}
	if entry.Value.Type() != integer {
		t.Fatalf("unary datum type: got %p, want current INT4 %p", entry.Value.Type(), integer)
	}
	// ArithmeticExpr enumerates its literal Left and omitted Right in order.
	if len(entry.Inputs) != 2 {
		t.Fatalf("unary input arity: got %d inputs %#v, want literal Left and omitted Right", len(entry.Inputs), entry.Inputs)
	}
	input, omitted := entry.Inputs[0], entry.Inputs[1]
	if input.Usage != integer.Usage() || input.Value.Type() != integer || input.UnknownNull {
		t.Fatalf("unary literal input: got %#v, want current INT4 usage %+v and non-NULL INT4 datum", input, integer.Usage())
	}
	if value, err := input.Value.Integer(); err != nil || value != 7 {
		t.Fatalf("unary literal value: got %d err=%v, want 7", value, err)
	}
	if omitted.Usage != (sqltype.Usage{}) || omitted.Value.Type() != nil || omitted.UnknownNull {
		t.Fatalf("unary omitted Right: got %#v, want zero usage/value and UnknownNull=false", omitted)
	}
	retainedCallable := false
	for _, dependency := range folds.Dependencies {
		if dependency.Descriptor == callable && dependency.Ref.Address == callable.Address() && dependency.Ref.Domain() == e.Catalog.Domain && dependency.Ref.Epoch == e.CatalogRead().Epoch() && dependency.Stamp == callable.Revision() {
			retainedCallable = true
		}
	}
	if !retainedCallable {
		t.Fatalf("accepted fold omitted current int4um dependency: got %+v, want descriptor %+v stamp %d", folds.Dependencies, callable.Address(), callable.Revision())
	}
	dependencies := append([]catalog.ExpressionDependency(nil), accepted.Dependencies...)
	a, b := replicaUnitIndex(t, e, "ri_a"), replicaUnitIndex(t, e, "ri_b")
	heap := e.Storage.Relations[old.Address()]
	aRoot, bRoot := e.Storage.IndexRoots[a.Address()], e.Storage.IndexRoots[b.Address()]
	rejected := replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_b")
	rejected.Actions = append(rejected.Actions, parser.AlterTableAction{Action: parser.SetReplicaIdentity, ReplicaIdentity: parser.ReplicaIdentityIndex, ReplicaIndex: parser.Identifier{Text: "ri_missing"}})
	_, err = sequencePreparedExecute(e, s, rejected, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" {
		t.Fatal("rejected selection", err)
	}
	if description.Analysis != accepted || !valid(accepted) || !reflect.DeepEqual(accepted.Dependencies, dependencies) || description.Folds != owner || owner.Current().Generation != folds.Generation || &owner.Current().Entries[0] != &folds.Entries[0] {
		t.Fatal("rejected selection changed accepted analysis/generation/backing")
	}
	if !samePublishedFold(entry, preparedFoldEntry(t, description, sqltype.Int4, "-7")) {
		t.Fatal("rejected selection mutated retained unary provenance or inputs")
	}
	if e.CatalogRead().MustRelation(old.Address()) != before || replicaUnitIndex(t, e, "ri_a") != a || replicaUnitIndex(t, e, "ri_b") != b || e.Storage.Relations[old.Address()] != heap || e.Storage.IndexRoots[a.Address()] != aRoot || e.Storage.IndexRoots[b.Address()] != bRoot {
		t.Fatal("rejected selection changed descriptor/storage authority")
	}
	execute()
	if description.Analysis != accepted || owner.Current().Generation != folds.Generation || &owner.Current().Entries[0] != &folds.Entries[0] {
		t.Fatal("unchanged definition did not retain actual accepted backing")
	}
	if !samePublishedFold(entry, preparedFoldEntry(t, description, sqltype.Int4, "-7")) {
		t.Fatal("reused unary fold changed its immutable provenance or inputs")
	}
	preparedUnitStatement(t, e, s, replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_b"))
	current := e.CatalogRead().MustRelation(old.Address())
	if current.Revision() <= before.Revision() || valid(accepted) {
		t.Fatal("same-mode switch retained the accepted parent stamp")
	}
	execute()
	if description.Analysis == accepted || !valid(description.Analysis) || !hasParentStamp(description.Analysis, current) || description.Folds.Analysis != description.Analysis || !reflect.DeepEqual(accepted.Dependencies, dependencies) {
		t.Fatal("successful switch did not publish fresh analysis or mutated its predecessor")
	}
	if replicaUnitIndex(t, e, "ri_a").SQLIndex.ReplicaIdentity || !replicaUnitIndex(t, e, "ri_b").SQLIndex.ReplicaIdentity || e.Storage.Relations[old.Address()] != heap || e.Storage.IndexRoots[a.Address()] != aRoot || e.Storage.IndexRoots[b.Address()] != bRoot {
		t.Fatal("same-mode switch changed heap/index roots or selected the wrong index")
	}
}

func TestReplicaIdentityCatalogMetadata(t *testing.T) {
	e, s, _ := replicaUnitFixture(t)
	view := e.CatalogRead()
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2610})
	if len(source.Columns) != 21 || source.Columns[14].Name != "indisreplident" || source.Columns[14].Usage.Type.ID() != sqltype.Bool || source.Attributes[14] != 15 || source.Columns[14].Nullable || pgcatalog.AttributeNumber(2610, "indisreplident", 15) != 15 {
		t.Fatal("identity source contract", source.Columns)
	}
	preparedUnitStatement(t, e, s, replicaUnitAlter(parser.ReplicaIdentityIndex, "ri_a"))
	view = e.CatalogRead()
	for _, name := range []string{"ri_a", "ri_b", "ri_c"} {
		idx := replicaUnitIndex(t, e, name)
		facts, _, err := index.ProjectIndexMetadata(t.Context(), view, e.Catalog.Domain.Ref(view.Epoch(), idx.Address()))
		if err != nil {
			t.Fatal(err)
		}
		if facts.IndIsReplIdent != (name == "ri_a") || facts.IndNatts != 2 || facts.IndNKeyAtts != 1 || !facts.IndImmediate || !reflect.DeepEqual(facts.IndKey, []int16{1, 2}) {
			t.Fatal("identity metadata lost actual selection or full index facts", name, facts)
		}
	}
	for _, tc := range []struct {
		kind catalog.RelationKind
		mode parser.ReplicaIdentityMode
	}{{catalog.TableRelation, parser.ReplicaIdentityDefault}, {catalog.PartitionedRelation, parser.ReplicaIdentityDefault}, {catalog.MaterializedViewRelation, parser.ReplicaIdentityDefault}, {catalog.CatalogRelation, parser.ReplicaIdentityNothing}, {catalog.IndexRelation, parser.ReplicaIdentityNothing}} {
		r := catalog.Relation{RelationKind: tc.kind}
		if r.EffectiveReplicaIdentity() != tc.mode {
			t.Fatal(tc)
		}
	}
}
