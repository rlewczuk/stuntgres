package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
)

func compoundSharedPresentation(t *testing.T, before, after *storage.IndexRoot) {
	t.Helper()
	if before == after || before.Gin == after.Gin || len(before.Gin.Columns) != len(after.Gin.Columns) || len(before.Gin.Pending) != len(after.Gin.Pending) {
		t.Fatal("presentation omitted live wrapper replacement")
	}
	if len(before.Entries) > 0 && &before.Entries[0] != &after.Entries[0] {
		t.Fatal("presentation rebuilt produced entries")
	}
	for portion, columns := range [2][]storage.GinColumn{before.Gin.Columns, before.Gin.Pending} {
		current := after.Gin.Columns
		if portion == 1 {
			current = after.Gin.Pending
		}
		for i, a := range columns {
			b := current[i]
			if a.Binding.Usage != b.Binding.Usage || len(a.Tokens) != len(b.Tokens) || len(a.NonNull) != len(b.NonNull) {
				t.Fatal("presentation changed scalar portion shape", portion, i)
			}
			if a.Binding.Class != before.Gin.Columns[i].Binding.Class || b.Binding.Class != after.Gin.Columns[i].Binding.Class {
				t.Fatal("presentation left main/pending on different class generations", portion, i)
			}
			if len(a.Tokens) > 0 && &a.Tokens[0] != &b.Tokens[0] || len(a.NonNull) > 0 && &a.NonNull[0] != &b.NonNull[0] {
				t.Fatal("presentation copied actual dictionary/nonnull backing", portion, i)
			}
			for j, list := range a.Tokens {
				if &list.Postings[0] != &b.Tokens[j].Postings[0] {
					t.Fatal("presentation copied exact posting list backing", portion, i, j)
				}
			}
			if i < 4 && a.Binding.Class.Support(1).Authority != b.Binding.Class.Support(1).Authority {
				t.Fatal("presentation reminted original installed authority", portion, i)
			}
		}
	}
}

func compoundExtensionPresentationProof(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			e, idx, original := compoundEngine(t, populated)
			enginetest.Run(t, e, "CREATE SCHEMA compound_cohort_dest")
			view := e.CatalogRead()
			before, entries := ginCopyPayload(original.Gin), gistCopyEntries(original)
			if !reflect.DeepEqual(original.Gin, before) || !reflect.DeepEqual(original.Entries, entries) {
				t.Fatal("retained-root snapshot changed original shape before any cohort operation")
			}
			oldHelper := original.Gin.Columns[0].Binding.Class.Support(2)
			session := enginetest.StartSession(t, e, nil)
			tx := enginetest.TransactionBegin(t, session)
			enginetest.SessionSQL(t, session, "INSERT INTO compound_private VALUES(8,'111','10','m','0.02','{2}','private:1')")
			writes := session.Transactions.Writes[idx.SQLIndex.Parent]
			enginetest.SessionSQL(t, session, "ALTER EXTENSION btree_gin SET SCHEMA compound_cohort_dest")
			e.Mu.Lock()
			privateView := e.PrivateCatalogLocked(session.Transactions.Catalog)
			privateIndex := privateView.MustRelation(idx.Address())
			privateRoot := index.EffectiveRoot(e.Storage.IndexRoots, session.Transactions.Catalog, idx.Address())
			e.Mu.Unlock()
			compoundSharedPresentation(t, original, privateRoot)
			for column := range 4 {
				selected := ginClass(t, privateView, ids.OID(privateIndex.SQLIndex.Keys[column].Class.OID()))
				if privateIndex.SQLIndex.Keys[column].Class != selected || privateRoot.Gin.Columns[column].Binding.Class != selected {
					t.Fatal("private cohort lost live key/root stamps", column)
				}
			}
			nextWrites := session.Transactions.Writes[idx.SQLIndex.Parent]
			if len(writes) != len(nextWrites) || &writes[0].Values[0] != &nextWrites[0].Values[0] {
				t.Fatal("cohort relocation reconstructed private values")
			}
			if output, ok := writes[0].IndexOutputs[idx.Address()]; ok {
				next := nextWrites[0].IndexOutputs[idx.Address()]
				if next.Index != privateIndex || !slices.Equal(output.Keys, next.Keys) || len(output.Keys) > 0 && &output.Keys[0] != &next.Keys[0] {
					t.Fatal("cohort relocation lost owned produced-key backing")
				}
			}
			if e.Storage.IndexRoots[idx.Address()] != original || e.CatalogRead().State() != view.State() {
				t.Fatal("private cohort escaped to committed owner")
			}
			enginetest.TransactionEnd(t, session, tx, false)
			if e.Storage.IndexRoots[idx.Address()] != original || e.CatalogRead().State() != view.State() {
				t.Fatal("rolled-back cohort changed original root/catalog")
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := enginetest.Execute(e, canceled, enginetest.Statement(t, "ALTER EXTENSION btree_gin SET SCHEMA compound_cohort_dest"), nil)
			if !errors.Is(err, context.Canceled) || e.Storage.IndexRoots[idx.Address()] != original || e.CatalogRead().State() != view.State() {
				t.Fatal("canceled cohort published any candidate", err)
			}
			enginetest.Run(t, e, "ALTER EXTENSION btree_gin SET SCHEMA compound_cohort_dest")
			relocatedIndex, relocated := ginInstalledRoot(t, e, "compound_private_i")
			compoundSharedPresentation(t, original, relocated)
			helper := relocated.Gin.Columns[0].Binding.Class.Support(2)
			if helper.Routine != oldHelper.Routine || helper.RoutineRevision == oldHelper.RoutineRevision || helper.Authority != oldHelper.Authority {
				t.Fatal("cohort omitted real helper revision or replaced original authority")
			}
			if _, err := routine.BindClassSupport(e.CatalogRead(), idx.SQLIndex.Keys[0].Class); err == nil {
				t.Fatal("cohort laundered detached source support capture")
			}
			for _, name := range []string{"bit_ops", "varbit_ops", "enum_ops", "money_ops"} {
				priorIndex, prior := ginInstalledRoot(t, e, "compound_private_i")
				enginetest.Run(t, e, "ALTER EXTENSION btree_gin DROP OPERATOR CLASS compound_cohort_dest."+name+" USING gin")
				detachedIndex, detached := ginInstalledRoot(t, e, "compound_private_i")
				compoundSharedPresentation(t, prior, detached)
				enginetest.Run(t, e, "INSERT INTO compound_private VALUES(8,'111','10','m','0.02','{2}','private:1')")
				_, written := ginInstalledRoot(t, e, "compound_private_i")
				if len(written.Entries) != len(prior.Entries)+1 {
					t.Fatal("detached genuine class lost maintained scalar postings")
				}
				enginetest.Run(t, e, "DELETE FROM compound_private WHERE id=8")
				enginetest.Run(t, e, "ALTER EXTENSION btree_gin ADD OPERATOR CLASS compound_cohort_dest."+name+" USING gin")
				if priorIndex == detachedIndex {
					t.Fatal("membership did not replace actual current key captures")
				}
			}
			for _, action := range []string{"DROP", "ADD"} {
				_, prior := ginInstalledRoot(t, e, "compound_private_i")
				enginetest.Run(t, e, "ALTER EXTENSION btree_gin "+action+" FUNCTION compound_cohort_dest.gin_extract_value_bit(bit,internal)")
				_, next := ginInstalledRoot(t, e, "compound_private_i")
				compoundSharedPresentation(t, prior, next)
			}
			if !reflect.DeepEqual(original.Gin, before) || !reflect.DeepEqual(original.Entries, entries) {
				t.Fatal("cohort operations mutated immutable original capture")
			}
			if relocatedIndex.Address() != idx.Address() {
				t.Fatal("cohort relocation replaced logical index identity")
			}
			if populated {
				// Produce an authentic predecessor-to-presented class transition first;
				// then change only the fifth member or its backing executable revision.
				beforeIndex, beforeRoot := ginInstalledRoot(t, e, "compound_private_i")
				beforeView := e.CatalogRead()
				enginetest.Run(t, e, "ALTER OPERATOR CLASS compound_cohort_dest.bit_ops USING gin RENAME TO compound_cohort_bit")
				afterIndex, afterRoot := ginInstalledRoot(t, e, "compound_private_i")
				afterView := e.CatalogRead()
				class := afterIndex.SQLIndex.Keys[0].Class
				member, _ := afterView.FamilyOperator(ids.OID(class.FamilyOID()), 1560, 1560, 5, 's')
				operator, _ := afterView.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: member.Operator})
				procedure, _ := afterView.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: operator.Function})
				for _, kind := range []string{"member", "operator", "routine"} {
					state := catalog.CloneState(afterView.State())
					switch kind {
					case "member":
						changed := *member
						changed.ObjectRevision = member.Revision() + 1
						state.SetDescriptor(changed.Address(), &changed)
					case "operator":
						changed := *operator
						changed.ObjectRevision = operator.Revision() + 1
						state.SetDescriptor(changed.Address(), &changed)
					case "routine":
						changed := *procedure
						changed.ObjectRevision = procedure.Revision() + 1
						state.SetDescriptor(changed.Address(), &changed)
					}
					badView := afterView.WithState(state)
					metadata, _ := badView.IndexClass(ids.OID(class.OID()))
					fresh, err := routine.BindClassSupport(badView, metadata)
					if err != nil {
						t.Fatal("tamper fixture must reach actual presentation certificate", kind, err)
					}
					candidate, definition := *afterIndex, *afterIndex.SQLIndex
					definition.Keys = slices.Clone(definition.Keys)
					definition.Keys[0].Class = fresh
					candidate.SQLIndex = &definition
					parent := beforeView.MustRelation(idx.SQLIndex.Parent)
					moved, err := index.RebindGinPresentationRoot(beforeView, badView, parent, parent, beforeIndex, &candidate, beforeRoot)
					if err == nil || moved != nil {
						t.Fatal("first four strategies hid changed fifth executable authority", kind, moved, err)
					}
					if e.Storage.IndexRoots[idx.Address()] != afterRoot {
						t.Fatal("rejected fifth-member transport published a root")
					}
				}
			}
		})
	}
}
