package stored_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestRoutineStoredReplacementPublication(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE FUNCTION scalar_publication(bigint) RETURNS integer IMMUTABLE RETURN $1::integer+1",
		"CREATE TABLE scalar_consumer(v integer DEFAULT scalar_publication(4),w integer DEFAULT scalar_publication(5),CHECK(scalar_publication(v)>0))",
		"CREATE INDEX scalar_key ON scalar_consumer(scalar_publication(v))",
		"CREATE INDEX scalar_predicate ON scalar_consumer(v) WHERE scalar_publication(v)>100",
		"CREATE TABLE scalar_unrelated(v integer)",
		"INSERT INTO scalar_consumer(v,w) VALUES(101,102),(103,104)",
	} {
		enginetest.Run(t, e, q)
	}
	source := e.CatalogRead()
	before := checkedDefaultOwnerProcedure(t, source, "scalar_publication")
	oldTable := transformStoredRelation(t, source, "scalar_consumer")
	unrelated := transformStoredRelation(t, source, "scalar_unrelated")
	oldDefaults := make(map[int32]catalog.ColumnDefault, len(oldTable.ColumnMeta.Defaults))
	for attribute, definition := range oldTable.ColumnMeta.Defaults {
		oldDefaults[attribute] = definition
	}
	oldChecks := slices.Clone(oldTable.Constraints.Check)
	oldDeps := slices.Clone(oldChecks[0].Dependencies)
	sourceStore := e.Storage.Relations[oldTable.Address()]
	sourceRoots := make(map[ids.ObjectAddress]*storage.IndexRoot)
	for _, name := range []string{"scalar_key", "scalar_predicate"} {
		idx := transformStoredRelation(t, source, name)
		root := e.Storage.IndexRoots[idx.Address()]
		if root == nil || len(root.Entries) != 2 {
			t.Fatal("missing genuinely populated source index", name)
		}
		sourceRoots[idx.Address()] = root
	}
	enginetest.Run(t, e, "CREATE OR REPLACE FUNCTION scalar_publication(bigint) RETURNS integer LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RETURN $1+2; END$$")
	published := e.CatalogRead()
	after := checkedDefaultOwnerProcedure(t, published, "scalar_publication")
	if e.Storage.Relations[oldTable.Address()] != sourceStore {
		t.Fatal("routine publication rewrote parent storage")
	}
	for address, root := range sourceRoots {
		if e.Storage.IndexRoots[address] != root {
			t.Fatal("routine publication rebuilt a populated index")
		}
	}
	if exec.SameRoutineDeclaration(before, after) {
		t.Fatal("replacement weakened executable declaration identity")
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(published, before); err == nil {
		t.Fatal("ordinary stale carrier acquired successor authority")
	}
	if published.Get(unrelated.Address()) != unrelated {
		t.Fatal("replacement copied an unrelated descriptor")
	}
	// Reconstruct the genuine pre-refresh publication boundary using the real
	// checked successor, not a mock declaration or a second SQL binder.
	state := catalog.CloneState(source.State())
	state.SetDescriptor(after.Address(), after)
	target := source.WithState(state)
	e.Mu.Lock()
	publication, err := stored.RefreshStoredScalarRoutine(source, target, before, after, e.Catalog.RevisionLocked)
	e.Mu.Unlock()
	changed := publication.Descriptors()
	if err != nil || len(changed) != 3 {
		t.Fatal("authentic scalar publication", len(changed), err)
	}
	completed := catalog.CloneState(target.State())
	for _, descriptor := range changed {
		old := source.Get(descriptor.Address()).(*catalog.Relation)
		next := descriptor.(*catalog.Relation)
		if next == old || next.Revision() == old.Revision() {
			t.Fatal("changed scalar owner retained original descriptor/stamp")
		}
		completed.SetDescriptor(next.Address(), next)
	}
	view := target.WithState(completed)
	sourceStores := map[ids.ObjectAddress]*storage.Relation{oldTable.Address(): sourceStore}
	for _, descriptor := range changed {
		idx := descriptor.(*catalog.Relation)
		if idx.SQLIndex == nil {
			continue
		}
		input := index.RoutineRootInput{Publication: publication, Target: view, Index: idx, Committed: source, CommittedRoots: sourceRoots, CommittedStores: sourceStores, Store: sourceStore}
		candidate, err := index.PreserveRoutineRoot(input)
		if err != nil || candidate.Root != sourceRoots[idx.Address()] || !candidate.MatchesIndex(idx, sourceStore) {
			t.Fatal("populated source receipt", err)
		}
		if index.SameIndexKeySemantics(source.MustRelation(idx.Address()), idx) {
			t.Fatal("routine receipt weakened strict key identity")
		}
		receipts := map[ids.ObjectAddress]index.RootCandidate{idx.Address(): candidate}
		if !index.RoutineSourcesMatch(receipts, source, sourceRoots, sourceStores) {
			t.Fatal("authentic source receipt rejected")
		}
		routinePublicationReceiptNegatives(t, input, candidate)
	}
	for _, descriptor := range changed {
		if err := view.Owner().Provider(descriptor.Address().ClassID).Validate(view, descriptor); err != nil {
			t.Fatal("completed provider", err)
		}
		found := false
		if err := stored.VisitDefinitionExpressions(descriptor, func(node expr.Expr) {
			plan.WalkExpression(node, func(node expr.Expr) {
				if call, ok := node.(plan.SQLRoutineExpr); ok && call.Ref.Address == before.Address() {
					found = true
					if call.Procedure != after || call.RoutineReference != after || call.Ref.Domain() != source.Owner().Domain || call.Ref.Epoch != source.Epoch() || call.ValidateStoredProvider() != nil {
						t.Fatal("publication split callable/ref/provider/usage authority")
					}
				}
			})
		}); err != nil || !found {
			t.Fatal("missing real scalar owner capture", err)
		}
	}
	nextTable := view.MustRelation(oldTable.Address())
	for _, dep := range nextTable.Constraints.Check[0].Dependencies {
		if dep.Ref.Address == before.Address() && (dep.Revision != after.Revision() || dep.Purpose != catalog.DefinitionDependency) {
			t.Fatal("CHECK manifest did not move with its executable")
		}
	}
	if view.Get(unrelated.Address()) != unrelated || target.Get(oldTable.Address()) != oldTable || !reflect.DeepEqual(oldTable.ColumnMeta.Defaults, oldDefaults) || !reflect.DeepEqual(oldTable.Constraints.Check, oldChecks) || !slices.Equal(oldTable.Constraints.Check[0].Dependencies, oldDeps) {
		t.Fatal("publication mutated source/target/default/CHECK backing")
	}
	if e.CatalogRead().State() != published.State() {
		t.Fatal("private refresh installed a root")
	}

	foreign := engine.New().CatalogRead()
	for _, kind := range []string{"nil-source", "nil-target", "nil-before", "nil-after", "forged-before", "forged-after", "foreign-source", "old-epoch", "missing-source", "missing-successor", "changed-source-stamp", "changed-successor-acl", "changed-target-owner", "foreign-call", "stale-call", "missing-dependency", "wrong-purpose", "wrong-revision", "wrong-result", "wrong-input", "wrong-collation", "wrong-provider", "failed-revision"} {
		t.Run(kind, func(t *testing.T) {
			s, dest, predecessor, successor := source, target, before, after
			sourceState, targetState := catalog.CloneState(source.State()), catalog.CloneState(target.State())
			copy := *oldTable
			revision := e.Catalog.RevisionLocked
			switch kind {
			case "nil-source":
				s = catalog.Read{}
			case "nil-target":
				dest = catalog.Read{}
			case "nil-before":
				predecessor = nil
			case "nil-after":
				successor = nil
			case "forged-before":
				p := *before
				predecessor = &p
			case "forged-after":
				p := *after
				successor = &p
			case "foreign-source":
				s = foreign
			case "old-epoch":
				s = source.WithState(catalog.NewState(source.Epoch() + 1))
			case "missing-source":
				sourceState.RemoveDescriptor(before.Address())
				s = source.WithState(sourceState)
			case "changed-source-stamp":
				p := *before
				p.ObjectRevision++
				sourceState.SetDescriptor(p.Address(), &p)
				s = source.WithState(sourceState)
			case "missing-successor":
				targetState.RemoveDescriptor(after.Address())
				dest = target.WithState(targetState)
			case "changed-successor-acl":
				p := *after
				p.ACL.Explicit = !p.ACL.Explicit
				targetState.SetDescriptor(p.Address(), &p)
				dest, successor = target.WithState(targetState), &p
			case "changed-target-owner":
				copy.ObjectRevision++
				targetState.SetDescriptor(copy.Address(), &copy)
				dest = target.WithState(targetState)
			case "wrong-purpose", "wrong-revision":
				copy.Constraints.Check = slices.Clone(oldTable.Constraints.Check)
				copy.Constraints.Check[0].Dependencies = slices.Clone(oldDeps)
				for i := range copy.Constraints.Check[0].Dependencies {
					dep := &copy.Constraints.Check[0].Dependencies[i]
					if dep.Ref.Address == before.Address() {
						if kind == "wrong-purpose" {
							dep.Purpose = catalog.OperandDependency
						} else {
							dep.Revision++
						}
					}
				}
			case "failed-revision":
				allocated := 0
				revision = func() (ids.DefinitionRevision, error) {
					allocated++
					if allocated == 2 {
						return 0, errors.New("revision unavailable")
					}
					return e.Catalog.RevisionLocked()
				}
			default:
				copy.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault, len(oldDefaults))
				for attribute, definition := range oldDefaults {
					copy.ColumnMeta.Defaults[attribute] = definition
				}
				attribute := oldTable.Attributes[0]
				definition := copy.ColumnMeta.Defaults[attribute]
				call, ok := definition.Expression.(plan.SQLRoutineExpr)
				if !ok {
					t.Fatal("expected genuine direct SQL DEFAULT capture")
				}
				switch kind {
				case "foreign-call":
					call.Ref = foreign.Owner().Domain.Ref(call.Ref.Epoch, call.Ref.Address)
				case "stale-call":
					call.Ref.Epoch++
				case "missing-dependency":
					definition.Dependencies = nil
				case "wrong-result":
					boolean, _ := source.LookupType(16)
					call.Usage = boolean.Usage()
				case "wrong-input":
					boolean, _ := source.LookupType(16)
					call.Signature.Inputs = []sqltype.Usage{boolean.Usage()}
				case "wrong-collation":
					collation := source.Get(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 950}).(*catalog.Collation)
					var err error
					call.InputCollation, err = collation.Usage()
					if err != nil {
						t.Fatal(err)
					}
				case "wrong-provider":
					p := *before
					body := *p.SQL
					body.Source = "SELECT 99"
					p.SQL = &body
					call.Procedure, call.RoutineReference = &p, &p
				}
				definition.Expression = call
				copy.ColumnMeta.Defaults[attribute] = definition
			}
			if kind == "wrong-purpose" || kind == "wrong-revision" || kind == "foreign-call" || kind == "stale-call" || kind == "missing-dependency" || kind == "wrong-result" || kind == "wrong-input" || kind == "wrong-collation" || kind == "wrong-provider" {
				sourceState.SetDescriptor(copy.Address(), &copy)
				targetState.SetDescriptor(copy.Address(), &copy)
				s, dest = source.WithState(sourceState), target.WithState(targetState)
			}
			e.Mu.Lock()
			result, failure := stored.RefreshStoredScalarRoutine(s, dest, predecessor, successor, revision)
			e.Mu.Unlock()
			if failure == nil || result != nil {
				t.Fatal("invalid publication returned an installable result", kind)
			}
			if e.CatalogRead().State() != published.State() || source.Get(oldTable.Address()) != oldTable || target.Get(oldTable.Address()) != oldTable || !reflect.DeepEqual(oldTable.ColumnMeta.Defaults, oldDefaults) || !slices.Equal(oldTable.Constraints.Check[0].Dependencies, oldDeps) {
				t.Fatal("failed candidate changed source, caller target, or published root")
			}
		})
	}
	routinePublicationIndexLifecycle(t, e, published, oldTable, sourceRoots)
	t.Run("populated-preflight-no-rebuild", routinePublicationNoRebuild)
}

func TestRoutineStoredReplacementOutputShape(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE FUNCTION scalar_tuple(OUT a integer,OUT b text) LANGUAGE SQL AS $$SELECT 5,'old'::text$$",
		"CREATE TABLE scalar_tuple_rows(v integer DEFAULT (scalar_tuple()).a,w text DEFAULT (scalar_tuple()).b)",
	} {
		enginetest.Run(t, e, q)
	}
	source := e.CatalogRead()
	before := checkedDefaultOwnerProcedure(t, source, "scalar_tuple")
	table := transformStoredRelation(t, source, "scalar_tuple_rows")
	definition, shape := before.SQL, before.SQL.Result
	members := []sqltype.CompositeMember{shape.Type.CompositeMember(0), shape.Type.CompositeMember(1)}
	defaults := table.ColumnMeta.Defaults
	enginetest.Run(t, e, "CREATE OR REPLACE FUNCTION scalar_tuple(OUT a integer,OUT b text) BEGIN ATOMIC SELECT 9,'new'::text; END")
	published := e.CatalogRead()
	after := checkedDefaultOwnerProcedure(t, published, "scalar_tuple")
	next := published.MustRelation(table.Address())
	if after == before || after.SQL == definition || after.SQL.Result != shape || !after.SQL.MatchesOutputTuple(shape.Type) || exec.SameRoutineDeclaration(after, before) {
		t.Fatal("replacement lost exact output shape or reused executable authority")
	}
	if before.SQL != definition || before.SQL.Result != shape || shape.Type.CompositeMember(0) != members[0] || shape.Type.CompositeMember(1) != members[1] || source.Get(table.Address()) != table || !reflect.DeepEqual(table.ColumnMeta.Defaults, defaults) {
		t.Fatal("replacement mutated original tuple or stored consumer")
	}
	calls := 0
	if err := stored.VisitDefinitionExpressions(next, func(node expr.Expr) {
		plan.WalkExpression(node, func(node expr.Expr) {
			if call, ok := node.(plan.SQLRoutineExpr); ok && call.Ref.Address == after.Address() {
				calls++
				if call.Procedure != after || call.RoutineReference != after || call.Usage != shape || call.Signature.Result != shape || call.ValidateStoredProvider() != nil {
					t.Fatal("stored tuple fields split successor and exact output authority")
				}
			}
		})
	}); err != nil || calls != 2 {
		t.Fatal("missing real stored field captures", calls, err)
	}
	session := enginetest.StartSession(t, e, nil)
	op, err := session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, failure := e.Execute(op, enginetest.Statement(t, "CREATE OR REPLACE FUNCTION scalar_tuple(OUT a integer,OUT b bigint) LANGUAGE SQL AS $$SELECT 9,10::bigint$$"), 0, nil)
	op.Done()
	if result.Rows != nil {
		result.Rows.Close()
	}
	if failure == nil || e.CatalogRead().State() != published.State() || after.SQL.Result != shape || published.Get(next.Address()) != next {
		t.Fatal("incompatible output replacement changed published authority", failure)
	}
}
