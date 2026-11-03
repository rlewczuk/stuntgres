package core_test

import (
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestAmcheckGINPrivateAuthority(t *testing.T) {
	for _, damage := range []string{"healthy", "wrong_parent", "wrong_output", "wrong_key", "false_keys", "false_pending", "missing_output", "base", "delete_ref", "delete_store", "duplicate_successor", "not_maintained", "baseline"} {
		t.Run(damage, func(t *testing.T) {
			e, d, r := ginAmcheckUnitRoot(t)
			reader, _ := blockingUnitOwner(t, e)
			enginetest.SessionSQL(t, reader, "INSERT INTO t VALUES(7,'{8,8}','cat')")
			enginetest.SessionSQL(t, reader, "UPDATE t SET a='{9}' WHERE n=1")
			enginetest.SessionSQL(t, reader, "DELETE FROM t WHERE n=2")
			parent := e.Catalog.State.MustRelation(d.SQLIndex.Parent)
			store := *e.Storage.Relations[parent.Address()]
			store.IndexSource = e.Storage.Relations[parent.Address()].IndexIdentity()
			c := index.AmcheckCapture{Index: d, Parent: parent, IndexRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address()), ParentRef: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), parent.Address()), Root: r, Store: store, Frontier: e.Visibility.CommitSeq(), Writes: append([]storage.RowVersion(nil), reader.Transactions.Writes[parent.Address()]...)}
			deletion := reader.Transactions.Deletes[parent.Address()]
			if deletion != nil {
				c.Deletes = &visibility.PrivateDeletes{Rows: maps.Clone(deletion.Rows)}
			}
			if len(c.Writes) != 2 || c.Deletes == nil {
				t.Fatal("missing actual private authority", c)
			}
			// GIN owns an actual produced key and immutable placement decision,
			// including this ordinary attribute-only insertion.
			for _, row := range c.Writes {
				output, ok := row.IndexOutputs[d.Address()]
				if !ok || !output.Member || !output.GinPending || !index.OutputMatches(output, d) || !index.RowMaintainsIndex(row, d) {
					t.Fatal("missing genuine produced GIN admission", output)
				}
			}
			switch damage {
			case "wrong_parent":
				c.ParentRef.Address.ObjectID++
			case "wrong_output", "wrong_key", "false_keys", "false_pending":
				row := &c.Writes[0]
				out := row.IndexOutputs[d.Address()]
				out.Keys = append([]sqltype.Value(nil), out.Keys...)
				if damage == "wrong_output" {
					wrong := *d
					wrong.ObjectAddress.ObjectID++
					out.Index = &wrong
				}
				if damage == "wrong_key" {
					out.Keys[0] = row.Values[0]
				}
				if damage == "false_keys" {
					out.Member = false
				}
				if damage == "false_pending" {
					out.Member, out.Keys = false, nil
				}
				row.IndexOutputs = maps.Clone(row.IndexOutputs)
				row.IndexOutputs[d.Address()] = out
			case "missing_output":
				c.Writes[0].IndexOutputs = maps.Clone(c.Writes[0].IndexOutputs)
				delete(c.Writes[0].IndexOutputs, d.Address())
			case "not_maintained":
				// This real admission does not contain an index created after the rows.
				enginetest.SessionSQL(t, reader, "CREATE INDEX later_i ON t USING gin((a || ARRAY[0]))")
				for _, row := range reader.Transactions.Writes[parent.Address()] {
					for _, out := range row.IndexOutputs {
						if out.Index.Name() == "later_i" {
							d = out.Index
						}
					}
				}
				if d.Name() != "later_i" {
					t.Fatal("missing genuine candidate descriptor")
				}
				if err := amcheckSessionCall(t, e, reader, "SELECT gin_index_check('later_i')"); err != nil {
					t.Fatal("genuine private candidate", err)
				}
				for _, row := range reader.Transactions.Writes[parent.Address()] {
					output, ok := row.IndexOutputs[d.Address()]
					if !ok || !index.OutputMatches(output, d) || index.RowMaintainsIndex(row, d) {
						t.Fatal("missing genuine pre-index output authority")
					}
				}
				c.Index = d
				c.IndexRef.Address = d.Address()
				c.Root = index.EffectiveRoot(e.Storage.IndexRoots, reader.Transactions.Catalog, d.Address())
				c.Writes = append([]storage.RowVersion(nil), reader.Transactions.Writes[parent.Address()]...)
				for i := range c.Writes {
					c.Writes[i].IndexOutputs = maps.Clone(c.Writes[i].IndexOutputs)
					delete(c.Writes[i].IndexOutputs, d.Address())
				}
			case "base":
				c.Writes[0].Base = ids.VersionID(999999)
			case "delete_ref", "delete_store":
				for row, del := range c.Deletes.Rows {
					if damage == "delete_ref" {
						del.Ref.Address.ObjectID++
					} else {
						del.Storage = &storage.Relation{}
					}
					c.Deletes.Rows[row] = del
					break
				}
			case "duplicate_successor":
				c.Writes = append(c.Writes, c.Writes[0])
			case "baseline":
				next := ginAmcheckClone(r)
				next.Gin.Columns[0].Tokens[0].Postings = nil
				c.Root = next
			}
			// Verification consumes the same ordinary owner environment/host.
			scope := pgrowlocksUnitScope(t, e, reader)
			before := ginAmcheckClone(c.Root)
			beforeWrites := ginAmcheckCopyWrites(c.Writes)
			var beforeDeletes *visibility.PrivateDeletes
			if c.Deletes != nil {
				beforeDeletes = &visibility.PrivateDeletes{Rows: maps.Clone(c.Deletes.Rows)}
			}
			err := scope.WithMaintenanceOwner(parent, func(owner *exec.Context) error { return index.CheckGin(owner, c) })
			switch damage {
			case "healthy":
				if err != nil {
					t.Fatal(err)
				}
			case "wrong_parent", "wrong_output":
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("foreign authority", err)
				}
			default:
				amcheckUnitDiagnostic(t, err, sqlstate.Error{Code: "XX002", Message: `GIN posting invariant violated for index "` + d.Name() + `"`})
			}
			if !reflect.DeepEqual(c.Root, before) || !reflect.DeepEqual(c.Writes, beforeWrites) || !reflect.DeepEqual(c.Deletes, beforeDeletes) || scope.Op.Restricted != nil {
				t.Fatal("private verification changed retained root, writes, deletes or owner")
			}
		})
	}
	t.Run("callable_output_consumed", func(t *testing.T) {
		e, d, _ := ginAmcheckUnitRoot(t)
		enginetest.Run(t, e, "DROP INDEX i")
		enginetest.Run(t, e, "CREATE SEQUENCE gin_key_calls")
		enginetest.Run(t, e, "CREATE DOMAIN gin_key AS integer[] CHECK(nextval('gin_key_calls')>0)")
		enginetest.Run(t, e, "CREATE INDEX i ON t USING gin((a::gin_key))")
		d, r := btreeRoot(t, e, "i")
		if !d.SQLIndex.RequiresInsertionOutput() {
			t.Fatal("callable fixture does not demand insertion output")
		}
		reader, _ := blockingUnitOwner(t, e)
		enginetest.SessionSQL(t, reader, "INSERT INTO t VALUES(7,'{8,8}','cat')")
		rows := reader.Transactions.Writes[d.SQLIndex.Parent]
		out, ok := rows[0].IndexOutputs[d.Address()]
		if !ok || !out.Member || !index.OutputMatches(out, d) {
			t.Fatal("missing actual callable output", out)
		}
		before := enginetest.Tuples(enginetest.SessionSQL(t, reader, "SELECT last_value FROM gin_key_calls"))
		if err := amcheckSessionCall(t, e, reader, "SELECT gin_index_check('i')"); err != nil {
			t.Fatal(err)
		}
		after := enginetest.Tuples(enginetest.SessionSQL(t, reader, "SELECT last_value FROM gin_key_calls"))
		if !reflect.DeepEqual(before, after) || e.Storage.IndexRoots[d.Address()] != r {
			t.Fatal("checker replayed callable insertion recipe", before, after)
		}
		captured := append([]storage.RowVersion(nil), rows...)
		captured[0].IndexOutputs = maps.Clone(captured[0].IndexOutputs)
		delete(captured[0].IndexOutputs, d.Address())
		reader.Transactions.Writes[d.SQLIndex.Parent] = captured
		amcheckUnitDiagnostic(t, amcheckSessionCall(t, e, reader, "SELECT gin_index_check('i')"), sqlstate.Error{Code: "XX002", Message: `GIN posting invariant violated for index "i"`})
		enginetest.SequenceScalar(t, enginetest.StartSession(t, e, nil), "SELECT nextval('gin_key_calls')", before[0][0].(int64)+1)
	})
}

func ginAmcheckCopyWrites(rows []storage.RowVersion) []storage.RowVersion {
	copied := append([]storage.RowVersion(nil), rows...)
	for i := range copied {
		copied[i].Values = append([]sqltype.Value(nil), rows[i].Values...)
		copied[i].IndexOutputs = maps.Clone(rows[i].IndexOutputs)
		for address, out := range copied[i].IndexOutputs {
			out.Keys = append([]sqltype.Value(nil), out.Keys...)
			copied[i].IndexOutputs[address] = out
		}
	}
	return copied
}
