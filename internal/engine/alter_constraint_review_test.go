package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
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
	before, store := e.state, e.storage[old.address]
	backing := old.uniqueConstraints[0].Index
	root := e.indexRoots[backing]
	op := alterConstraintOperation(t, s)
	auth, err := op.Authorization()
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
	next, err := notNullCandidate(op, auth.catalog, old, actions[1], notNullDirect)
	if err != nil {
		t.Fatal(err)
	}
	next, stage, err := e.stageDroppedConstraintIndexes(scope, auth, nil, old, next, actions, nil)
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		t.Fatal(err)
	}
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: old.namespace, Name: "t_a_key"}
	if stage.lookupName(key) != (ids.ObjectAddress{}) || stage.removals[backing].ref.Address != backing || stage.touched[key] != backing {
		t.Fatal("retirement did not vacate private name with original publication fences")
	}
	next, err = e.stageAddedUnique(op, auth, stage, next, actions[0].Unique)
	if err != nil {
		t.Fatal(err)
	}
	added := next.uniqueConstraints[0]
	if err := stage.Replace(next); err != nil {
		t.Fatal(err)
	}
	if added.Name != "t_a_key" || added.Index == backing || added.OID == old.uniqueConstraints[0].OID || stage.lookupName(key) != added.Index || stage.touched[key] != backing {
		t.Fatal("effective generated name or replacement identity/fences lost", added)
	}
	e.mu.Lock()
	err = e.evolveStageIndexesLocked(stage, old.address, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, present := stage.descriptors[backing]; present || stage.removals[backing].ref.Address != backing {
		t.Fatal("later layout evolution resurrected retired backing")
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err != nil {
		t.Fatal(err)
	}
	if e.state != before || e.storage[old.address] != store || e.indexRoots[backing] != root || before.names[key] != backing || len(old.uniqueConstraints) != 1 {
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
	before, store := e.state, e.storage[old.address]
	for _, query := range []string{
		"ALTER TABLE t ALTER a SET NOT NULL,ADD PRIMARY KEY(a)",
		"ALTER TABLE t ADD PRIMARY KEY(a),DROP CONSTRAINT nn",
	} {
		raw, err := parser.Parse(query)
		if err != nil {
			t.Fatal(err)
		}
		err = verifyAlterPrimaryPrerequisites(old, raw[0].(*parser.AlterTable).Actions)
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
	if err := verifyAlterPrimaryPrerequisites(old, raw[0].(*parser.AlterTable).Actions); err != nil {
		t.Fatal("new key was prematurely bound against original shape", err)
	}
	if e.state != before || e.storage[old.address] != store || alterConstraintDescriptor(t, e, "t") != old || !old.notNullConstraints[0].NotValid || len(old.uniqueConstraints) != 0 {
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
			before, parentStore, childStore := e.state, e.storage[old.address], e.storage[child.address]
			ua, ub := old.uniqueConstraints[0].Index, old.uniqueConstraints[1].Index
			uaRoot, ubRoot := e.indexRoots[ua], e.indexRoots[ub]
			op := alterConstraintOperation(t, s)
			auth, err := op.Authorization()
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
					next, err = notNullCandidate(op, auth.catalog, next, action, notNullDirect)
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
				candidate := stage.descriptors[child.address].(*relationDescriptor)
				if len(candidate.foreignKeys) != 0 {
					t.Fatal("authorized child dependency retained")
				}
			} else {
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop constraint ub on table t because other objects depend on it", Detail: "constraint cfk on table c depends on index ub", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
				var got *sqlstate.Error
				if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
					t.Fatal("current DROP mode/diagnostic subject lost", got, err)
				}
				candidate := child
				if d, present := stage.descriptors[child.address]; present {
					candidate = d.(*relationDescriptor)
				}
				if !reflect.DeepEqual(candidate.foreignKeys, child.foreignKeys) {
					t.Fatal("earlier CASCADE removed later RESTRICT dependency")
				}
			}
			if stage.removals[ua].ref.Address != ua || stage.removals[ub].ref.Address != ub {
				t.Fatal("actual selected backing retirement missing")
			}
			if e.state != before || e.storage[old.address] != parentStore || e.storage[child.address] != childStore || e.indexRoots[ua] != uaRoot || e.indexRoots[ub] != ubRoot || len(child.foreignKeys) != 1 {
				t.Fatal("private mixed-mode traversal changed live catalog/storage/roots")
			}
		})
	}
}
