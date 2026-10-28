package core_test

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

func TestDeferredForeignKeyEventEligibilityAbsent(t *testing.T) {
	for _, tc := range []struct {
		name, match, parent, mutation string
	}{
		{"committed-note", "SIMPLE", "1,1", "UPDATE c SET note=1 WHERE note=0"},
		{"committed-key", "SIMPLE", "1,1", "UPDATE c SET a=a,note=1 WHERE note=0"},
		{"simple-all-null", "SIMPLE", "1,1", "UPDATE c SET a=NULL,b=NULL WHERE note=0"},
		{"simple-left-null", "SIMPLE", "1,1", "UPDATE c SET a=NULL,b=8 WHERE note=0"},
		{"simple-right-null", "SIMPLE", "1,1", "UPDATE c SET a=8,b=NULL WHERE note=0"},
		{"full-all-null", "FULL", "1,1", "UPDATE c SET a=NULL,b=NULL WHERE note=0"},
		{"parent-delete-all-null", "SIMPLE", "NULL,NULL", "DELETE FROM p WHERE marker=0"},
		{"parent-delete-partial-null", "SIMPLE", "1,NULL", "DELETE FROM p WHERE marker=0"},
		{"parent-update-all-null", "SIMPLE", "NULL,NULL", "UPDATE p SET a=2,b=2 WHERE marker=0"},
		{"parent-update-partial-null", "SIMPLE", "NULL,1", "UPDATE p SET a=2,b=2 WHERE marker=0"},
	} {
		for _, prefix := range []bool{false, true} {
			name := tc.name + "/empty"
			if prefix {
				name = tc.name + "/retained-prefix"
			}
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				s := sessionStart(t, e, nil)
				sessionSQL(t, s, "CREATE TABLE p(a INT,b INT,marker INT,UNIQUE(a,b))")
				sessionSQL(t, s, "CREATE TABLE c(a INT,b INT,note INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p(a,b) MATCH "+tc.match+" INITIALLY DEFERRED)")
				sessionSQL(t, s, "INSERT INTO p VALUES("+tc.parent+",0),(9,9,1)")
				if tc.parent == "1,1" {
					sessionSQL(t, s, "INSERT INTO c VALUES(1,1,0)")
				}
				transactionBegin(t, s)
				sessionSQL(t, s, "SET CONSTRAINTS fk DEFERRED")
				if prefix {
					sessionSQL(t, s, "INSERT INTO c VALUES(9,9,99)")
				}
				before := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
				savedModes := maps.Clone(before.ByConstraint)
				savedEvents := slices.Clone(before.Events)
				var savedPayload index.ForeignKeyEvent
				if prefix {
					if len(before.Events) != 1 || before.Events[0].ForeignKey == nil {
						t.Fatal("missing authentic prefix event")
					}
					savedPayload = *before.Events[0].ForeignKey
					savedPayload.Next.Values = slices.Clone(savedPayload.Next.Values)
				}
				sessionSQL(t, s, tc.mutation)
				after := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
				if len(after.Events) != len(before.Events) || !reflect.DeepEqual(after.Events, savedEvents) || !maps.Equal(after.ByConstraint, savedModes) || !maps.Equal(before.ByConstraint, savedModes) || after.AllSet != before.AllSet || after.AllDeferred != before.AllDeferred {
					t.Fatal("ineligible transition appended an event or changed saved modes")
				}
				if prefix && (&after.Events[0] != &before.Events[0] || after.Events[0].ForeignKey != before.Events[0].ForeignKey || !reflect.DeepEqual(*after.Events[0].ForeignKey, savedPayload)) {
					t.Fatal("suppressed admission detached or mutated retained event backing/payload")
				}
			})
		}
	}
}

func TestDeferredForeignKeyEventEligibilityAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, match, mutation, own string
		parentEvent                bool
	}{
		{"insert-valid", "SIMPLE", "INSERT INTO c VALUES(1,1,1)", "", false},
		{"insert-all-null", "SIMPLE", "INSERT INTO c VALUES(NULL,NULL,1)", "", false},
		{"insert-simple-partial", "SIMPLE", "INSERT INTO c VALUES(1,NULL,1)", "", false},
		{"insert-full-all-null", "FULL", "INSERT INTO c VALUES(NULL,NULL,1)", "", false},
		{"insert-full-partial", "FULL", "INSERT INTO c VALUES(1,NULL,1)", "", false},
		{"changed-valid", "SIMPLE", "UPDATE c SET a=2,b=2 WHERE note=0", "", false},
		{"changed-invalid", "SIMPLE", "UPDATE c SET a=8,b=8 WHERE note=0", "", false},
		{"update-full-partial", "FULL", "UPDATE c SET b=NULL WHERE note=0", "", false},
		{"own-inserted", "SIMPLE", "UPDATE c SET note=2 WHERE note=1", "INSERT INTO c VALUES(1,1,1)", false},
		{"own-changed", "SIMPLE", "UPDATE c SET note=2 WHERE note=1", "UPDATE c SET a=2,b=2,note=1 WHERE note=0", false},
		{"own-unchanged-update", "SIMPLE", "UPDATE c SET note=2 WHERE note=1", "UPDATE c SET note=1 WHERE note=0", false},
		{"parent-delete", "SIMPLE", "DELETE FROM p WHERE marker=0", "", true},
		{"parent-update", "SIMPLE", "UPDATE p SET a=3,b=3 WHERE marker=0", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE p(a INT,b INT,marker INT,UNIQUE(a,b))")
			sessionSQL(t, s, "CREATE TABLE c(a INT,b INT,note INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p(a,b) MATCH "+tc.match+" INITIALLY DEFERRED)")
			sessionSQL(t, s, "INSERT INTO p VALUES(1,1,0),(2,2,1)")
			sessionSQL(t, s, "INSERT INTO c VALUES(1,1,0)")
			child := enginetest.CatalogRef(t, e, e.CatalogRead(), "c")
			parent := enginetest.CatalogRef(t, e, e.CatalogRead(), "p")
			definition := e.CatalogRead().MustRelation(child.Address).Constraints.ForeignKeys[0]
			transactionBegin(t, s)
			if tc.own != "" {
				sessionSQL(t, s, tc.own)
				// Remove every earlier event; the next UPDATE must enlist its
				// own obligation even if its key equals the ultimate base key.
				sessionSQL(t, s, "SET CONSTRAINTS fk IMMEDIATE")
				sessionSQL(t, s, "SET CONSTRAINTS fk DEFERRED")
			}
			if len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)).Events) != 0 {
				t.Fatal("replacement witness inherited an earlier obligation")
			}
			scope, err := alterConstraintExecuteScope(t, e, s, tc.mutation)
			if err != nil {
				t.Fatal(err)
			}
			var row storage.RowVersion
			relation := child
			if tc.parentEvent {
				relation = parent
				row = e.Storage.Relations[parent.Address].Versions[0]
			} else {
				staged := scope.Stage.Rows[child.Address]
				if len(staged) != 1 {
					t.Fatal("missing actual staged child", staged)
				}
				row = staged[0]
				if tc.own != "" {
					rows, err := scope.CaptureForeignKeyRows(child, ids.ObjectAddress{})
					if err != nil {
						t.Fatal(err)
					}
					previous, ok := rows.Previous(row.Row)
					if !ok || previous.Born != 0 || previous.Version == row.Version || (tc.name != "own-inserted" && row.Base == 0) {
						t.Fatal("lost actual immediate own predecessor provenance", previous, row)
					}
				}
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			scope.Op.Done()
			modes := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
			if len(modes.Events) != 1 {
				t.Fatal("eligible transition did not append exactly one event", len(modes.Events))
			}
			event := modes.Events[0]
			payload := event.ForeignKey
			if payload == nil || event.Parent != relation || event.Constraint != definition.OID || event.Row != row.Row || event.Version != row.Version || payload.Child != child || payload.Parent != parent || payload.Index.Address != definition.Index || payload.ParentEvent != tc.parentEvent || payload.Owner.ID() == 0 {
				t.Fatal("event lost constraint, relation, actor or exact physical incarnation", event)
			}
			captured := payload.Next
			if tc.parentEvent {
				captured = payload.Old
			}
			if captured.Row != row.Row || captured.Version != row.Version || captured.Born != row.Born || captured.Base != row.Base || !reflect.DeepEqual(captured.Values, row.Values) || &captured.Values[0] != &row.Values[0] || captured.IndexOutputs != nil || captured.IndexAdmission != nil {
				t.Fatal("payload copied/mutated row values or retained command artifacts", captured, row)
			}
		})
	}
}

func TestDeferredForeignKeyEventEligibilityRetention(t *testing.T) {
	for _, tc := range []struct {
		mutation string
		count    int
	}{
		{"UPDATE c SET pid=NULL", 1},
		{"UPDATE c SET pid=1", 2},
		{"DELETE FROM c", 1},
	} {
		t.Run(tc.mutation, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE p(id INT PRIMARY KEY)")
			sessionSQL(t, s, "CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)")
			sessionSQL(t, s, "INSERT INTO p VALUES(1)")
			transactionBegin(t, s)
			sessionSQL(t, s, "SET CONSTRAINTS fk DEFERRED")
			sessionSQL(t, s, "INSERT INTO c VALUES(9)")
			before := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
			if len(before.Events) != 1 || before.Events[0].ForeignKey == nil {
				t.Fatal("missing original event")
			}
			payload := before.Events[0].ForeignKey
			saved := *payload
			saved.Next.Values = slices.Clone(payload.Next.Values)
			savedModes := maps.Clone(before.ByConstraint)
			sessionSQL(t, s, "SAVEPOINT retained")
			sessionSQL(t, s, tc.mutation)
			after := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
			if len(after.Events) != tc.count || after.Events[0].ForeignKey != payload || !reflect.DeepEqual(*payload, saved) || !maps.Equal(before.ByConstraint, savedModes) {
				t.Fatal("repair/deletion pruned or mutated authentic original event")
			}
			if tc.count == 1 && &after.Events[0] != &before.Events[0] {
				t.Fatal("non-enlisting transition detached saved event backing")
			}
			sessionSQL(t, s, "ROLLBACK TO retained")
			restored := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
			if !reflect.DeepEqual(before, restored) || &restored.Events[0] != &before.Events[0] || restored.Events[0].ForeignKey != payload {
				t.Fatal("savepoint did not restore exact event ownership")
			}
			sessionSQL(t, s, "DELETE FROM c")
			sessionSQL(t, s, "SET CONSTRAINTS fk IMMEDIATE")
			if len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions)).Events) != 0 || !reflect.DeepEqual(*payload, saved) || !maps.Equal(before.ByConstraint, savedModes) {
				t.Fatal("consumption retained work or mutated a saved source owner")
			}
		})
	}
}
