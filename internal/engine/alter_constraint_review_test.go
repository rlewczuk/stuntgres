package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAlterAddConstraintBackingRetirement(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(a INT,z INT,b INT,CONSTRAINT t_a_key UNIQUE(a))")
	sessionSQL(t, s, "ALTER TABLE t DROP z")
	sessionSQL(t, s, "INSERT INTO t VALUES(1,1)")
	old := alterConstraintDescriptor(t, e, "t")
	before, store := e.catalog.State, e.storage.Relations[old.Address()]
	backing := old.Constraints.Unique[0].Index
	root := e.storage.IndexRoots[backing]
	op := alterConstraintOperation(t, s)
	auth, err := operationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	raw, err := parser.Parse("ALTER TABLE t ADD UNIQUE(a),DROP CONSTRAINT t_a_key")
	if err != nil {
		t.Fatal(err)
	}
	actions := raw[0].(*parser.AlterTable).Actions
	next, err := table.NotNullCandidate(op, auth.Catalog, old, actions[1], table.NotNullDirect)
	if err != nil {
		t.Fatal(err)
	}
	next, stage, err := e.stageDroppedConstraintIndexes(scope, auth, nil, old, next, actions, nil)
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		t.Fatal(err)
	}
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: old.Namespace(), Name: "t_a_key"}
	removal, removed := stage.Removal(backing)
	observed, touched := stage.NameObservation(key)
	if stage.LookupName(key) != (ids.ObjectAddress{}) || !removed || removal.Ref.Address != backing || !touched || observed != backing {
		t.Fatal("retirement did not vacate private name with original publication fences")
	}
	next, err = e.stageAddedUnique(op, auth, stage, next, actions[0].Unique)
	if err != nil {
		t.Fatal(err)
	}
	added := next.Constraints.Unique[0]
	if err := stage.Replace(next); err != nil {
		t.Fatal(err)
	}
	observed, touched = stage.NameObservation(key)
	if added.Name != "t_a_key" || added.Index == backing || added.OID == old.Constraints.Unique[0].OID || stage.LookupName(key) != added.Index || !touched || observed != backing {
		t.Fatal("effective generated name or replacement identity/fences lost", added)
	}
	e.mu.Lock()
	err = e.evolveStageIndexesLocked(stage, old.Address(), nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	removal, removed = stage.Removal(backing)
	if _, present := stage.Find(backing); present || !removed || removal.Ref.Address != backing {
		t.Fatal("later layout evolution resurrected retired backing")
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err != nil {
		t.Fatal(err)
	}
	if e.catalog.State != before || e.storage.Relations[old.Address()] != store || e.storage.IndexRoots[backing] != root || before.NameAddress(key) != backing || len(old.Constraints.Unique) != 1 {
		t.Fatal("private retirement/replacement changed live catalog or physical root")
	}
}

func TestAlterAddConstraintPrimaryPrerequisite(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(z INT,a INT)")
	sessionSQL(t, s, "ALTER TABLE t DROP z")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	sessionSQL(t, s, "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a NOT VALID")
	old := alterConstraintDescriptor(t, e, "t")
	before, store := e.catalog.State, e.storage.Relations[old.Address()]
	for _, query := range []string{
		"ALTER TABLE t ALTER a SET NOT NULL,ADD PRIMARY KEY(a)",
		"ALTER TABLE t ADD PRIMARY KEY(a),DROP CONSTRAINT nn",
	} {
		raw, err := parser.Parse(query)
		if err != nil {
			t.Fatal(err)
		}
		err = table.VerifyAlterPrimaryPrerequisites(old, raw[0].(*parser.AlterTable).Actions)
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: `cannot create primary key on column "a"`, Detail: `The constraint "nn" on column "a" of table "t", marked NOT VALID, is incompatible with a primary key.`, Hint: "You might need to validate it using ALTER TABLE ... VALIDATE CONSTRAINT."}
		var got *sqlstate.Error
		if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
			t.Fatal("original sparse-attribute NN prerequisite lost", got, err)
		}
	}
	raw, err := parser.Parse("ALTER TABLE t ADD new_key INT,ADD PRIMARY KEY(new_key)")
	if err != nil {
		t.Fatal(err)
	}
	if err := table.VerifyAlterPrimaryPrerequisites(old, raw[0].(*parser.AlterTable).Actions); err != nil {
		t.Fatal("new key was prematurely bound against original shape", err)
	}
	if e.catalog.State != before || e.storage.Relations[old.Address()] != store || alterConstraintDescriptor(t, e, "t") != old || !old.Constraints.NotNull[0].NotValid || len(old.Constraints.Unique) != 0 {
		t.Fatal("preliminary admission changed immutable original catalog/storage")
	}
}

func TestAlterAddConstraintDropModeIsolation(t *testing.T) {
	for _, succeeds := range []bool{true, false} {
		name := "restrict_then_cascade"
		if !succeeds {
			name = "cascade_then_restrict"
		}
		t.Run(name, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(a INT,b INT,CONSTRAINT ua UNIQUE(a),CONSTRAINT ub UNIQUE(b))")
			sessionSQL(t, s, "CREATE TABLE c(b INT CONSTRAINT cfk REFERENCES t(b))")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,1)")
			sessionSQL(t, s, "INSERT INTO c VALUES(1)")
			old, child := alterConstraintDescriptor(t, e, "t"), alterConstraintDescriptor(t, e, "c")
			before, parentStore, childStore := e.catalog.State, e.storage.Relations[old.Address()], e.storage.Relations[child.Address()]
			ua, ub := old.Constraints.Unique[0].Index, old.Constraints.Unique[1].Index
			uaRoot, ubRoot := e.storage.IndexRoots[ua], e.storage.IndexRoots[ub]
			op := alterConstraintOperation(t, s)
			auth, err := operationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			query := "ALTER TABLE t DROP CONSTRAINT ua RESTRICT,DROP CONSTRAINT ub CASCADE,ADD CONSTRAINT ua UNIQUE(a)"
			if !succeeds {
				query = "ALTER TABLE t DROP CONSTRAINT ua CASCADE,DROP CONSTRAINT ub RESTRICT,ADD CONSTRAINT ua UNIQUE(a)"
			}
			raw, err := parser.Parse(query)
			if err != nil {
				t.Fatal(err)
			}
			// StartStatement serializes admission but does not attach a transaction
			// incarnation/ticket. Real dependency-owner locks require the SQL gate.
			if err := scope.gate(raw[0]); err != nil {
				t.Fatal(err)
			}
			actions := raw[0].(*parser.AlterTable).Actions
			next := old
			for _, action := range actions {
				if action.Action == parser.DropConstraint {
					next, err = table.NotNullCandidate(op, auth.Catalog, next, action, table.NotNullDirect)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			_, stage, err := e.stageDroppedConstraintIndexes(scope, auth, nil, old, next, actions, nil)
			defer e.DiscardCatalogStage(stage)
			if succeeds {
				if err != nil {
					t.Fatal("later authorized CASCADE leaked into earlier RESTRICT", err)
				}
				candidate := stage.Get(child.Address()).(*catalog.Relation)
				if len(candidate.Constraints.ForeignKeys) != 0 {
					t.Fatal("authorized child dependency retained")
				}
			} else {
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop constraint ub on table t because other objects depend on it", Detail: "constraint cfk on table c depends on index ub", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
				var got *sqlstate.Error
				if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
					t.Fatal("current DROP mode/diagnostic subject lost", got, err)
				}
				candidate := child
				if d, present := stage.Find(child.Address()); present {
					candidate = d.(*catalog.Relation)
				}
				if !reflect.DeepEqual(candidate.Constraints.ForeignKeys, child.Constraints.ForeignKeys) {
					t.Fatal("earlier CASCADE removed later RESTRICT dependency")
				}
			}
			removalA, removedA := stage.Removal(ua)
			removalB, removedB := stage.Removal(ub)
			if !removedA || !removedB || removalA.Ref.Address != ua || removalB.Ref.Address != ub {
				t.Fatal("actual selected backing retirement missing")
			}
			if e.catalog.State != before || e.storage.Relations[old.Address()] != parentStore || e.storage.Relations[child.Address()] != childStore || e.storage.IndexRoots[ua] != uaRoot || e.storage.IndexRoots[ub] != ubRoot || len(child.Constraints.ForeignKeys) != 1 {
				t.Fatal("private mixed-mode traversal changed live catalog/storage/roots")
			}
		})
	}
}
