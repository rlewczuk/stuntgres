package core_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func amcheckSessionCall(t *testing.T, e *core.Engine, s *session.Session, query string) error {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	result, err := e.Execute(op, enginetest.Statement(t, query), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	return err
}
func TestAmcheckPrivateAuthority(t *testing.T) {
	for _, damage := range []string{"implicit", "wrong_member", "wrong_key", "cross_unique", "own_unique", "missing_required", "outer_output"} {
		t.Run(damage, func(t *testing.T) {
			e, d, root := amcheckUnitRoot(t, true)
			var expression *catalog.Relation
			if damage == "missing_required" {
				enginetest.Run(t, e, "CREATE DOMAIN positive AS INT CHECK(VALUE>0)")
				enginetest.Run(t, e, "CREATE INDEX required_i ON t((n::positive)) WHERE n IS NOT NULL")
				expression, _ = btreeRoot(t, e, "required_i")
				if !expression.SQLIndex.RequiresInsertionOutput() {
					t.Fatal("fixture has no genuine required output")
				}
			}
			s := sessionStart(t, e, nil)
			tx := transactionBegin(t, s)
			defer transactionEnd(t, s, tx, false)
			sessionSQL(t, s, "INSERT INTO t VALUES(3,'c'),(4,'d')")
			if err := amcheckSessionCall(t, e, s, "SELECT bt_index_parent_check('i',true,true,true)"); err != nil {
				t.Fatal("healthy actual private union", err)
			}
			if e.Storage.IndexRoots[d.Address()] != root || len(root.Entries) != 3 {
				t.Fatal("DML rebuilt committed authority")
			}
			if damage == "implicit" {
				sessionSQL(t, s, "SAVEPOINT private")
				sessionSQL(t, s, "UPDATE t SET n=5 WHERE n=3")
				if err := amcheckSessionCall(t, e, s, "SELECT bt_index_parent_check('i',true,true,true)"); err != nil {
					t.Fatal(err)
				}
				sessionSQL(t, s, "DELETE FROM t WHERE n=5")
				if err := amcheckSessionCall(t, e, s, "SELECT bt_index_parent_check('i',true,true,true)"); err != nil {
					t.Fatal(err)
				}
				sessionSQL(t, s, "ROLLBACK TO private")
				if err := amcheckSessionCall(t, e, s, "SELECT bt_index_parent_check('i',true,true,true)"); err != nil {
					t.Fatal(err)
				}
				return
			}
			target := d
			if expression != nil {
				target = expression
			}
			rows := s.Transactions.Writes[d.SQLIndex.Parent]
			original := rows
			rows = append([]storage.RowVersion(nil), rows...)
			for i := range rows {
				rows[i].IndexOutputs = maps.Clone(rows[i].IndexOutputs)
				if rows[i].IndexOutputs == nil {
					rows[i].IndexOutputs = make(map[ids.ObjectAddress]storage.IndexOutput)
				}
			}
			output := storage.IndexOutput{Index: target, Member: true, Keys: []sqltype.Value{rows[0].Values[0]}}
			switch damage {
			case "wrong_member":
				output.Member = false
				output.Keys = nil
			case "wrong_key", "cross_unique":
				output.Keys = root.Entries[0].Keys
			case "own_unique":
				rows[1].IndexOutputs[d.Address()] = output
			case "outer_output":
				copy := *target
				definition := *target.SQLIndex
				definition.Parent.ObjectID++
				copy.SQLIndex = &definition
				output.Index = &copy
			case "missing_required":
				if _, ok := rows[0].IndexOutputs[target.Address()]; !ok {
					t.Fatal("insertion producer supplied no required output")
				}
				delete(rows[0].IndexOutputs, target.Address())
			}
			if damage != "missing_required" {
				rows[0].IndexOutputs[target.Address()] = output
			}
			s.Transactions.Mu.Lock()
			s.Transactions.Writes = maps.Clone(s.Transactions.Writes)
			s.Transactions.Writes[d.SQLIndex.Parent] = rows
			s.Transactions.Mu.Unlock()
			if damage == "cross_unique" || damage == "own_unique" {
				if err := amcheckSessionCall(t, e, s, "SELECT bt_index_check('i',false,false)"); err != nil {
					t.Fatal("checkunique false did not skip demanded uniqueness", err)
				}
				amcheckUnitDiagnostic(t, amcheckSessionCall(t, e, s, "SELECT bt_index_check('i',false,true)"), sqlstate.Error{Code: "XX002", Message: `index uniqueness is violated for index "i"`})
			} else if damage == "outer_output" {
				if err := amcheckSessionCall(t, e, s, "SELECT bt_index_check('i',true)"); !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("outer output adopted", err)
				}
			} else {
				block, offset, err := exec.PgrowlocksLogicalTID(rows[0].Version)
				if err != nil {
					t.Fatal(err)
				}
				query := "SELECT bt_index_check('" + target.Name() + "',true)"
				amcheckUnitDiagnostic(t, amcheckSessionCall(t, e, s, query), sqlstate.Error{Code: "XX001", Message: fmt.Sprintf("heap tuple (%d,%d) from table \"t\" lacks matching index tuple within index %q", block, offset, target.Name()), Hint: "Retrying verification using the function bt_index_parent_check() might provide a more specific error."})
			}
			if len(original) != 2 || original[0].Version != rows[0].Version || e.Storage.IndexRoots[d.Address()] != root || len(root.Entries) != 3 {
				t.Fatal("verification changed retained private/baseline authority")
			}
		})
	}
}

// The build consumes real pre-index private INSERT and UPDATE versions. It
// retains outputs even though their insertion admission cannot name this index.
func TestAmcheckPrivateDDLAuthority(t *testing.T) {
	e, d, committed := amcheckUnitRoot(t, false)
	s := sessionStart(t, e, nil)
	tx := transactionBegin(t, s)
	defer transactionEnd(t, s, tx, false)
	sessionSQL(t, s, "INSERT INTO t VALUES(4,'D'),(5,'E')")
	sessionSQL(t, s, "UPDATE t SET n=6,s='F' WHERE n=1")
	sessionSQL(t, s, "CREATE INDEX ddl_i ON t(lower(s)) WHERE n>1 AND n<>4")
	// Resolve using the same catalog authority captured for this private owner.
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	var built *catalog.Relation
	for _, row := range s.Transactions.Writes[d.SQLIndex.Parent] {
		for _, output := range row.IndexOutputs {
			if output.Index.Name() == "ddl_i" {
				built = output.Index
			}
		}
	}
	if built == nil || auth.Catalog.Get(built.Address()) != built {
		t.Fatal("DDL did not retain actual output authority")
	}
	baseline := index.EffectiveRoot(e.Storage.IndexRoots, s.Transactions.Catalog, built.Address())
	if baseline == nil || len(baseline.Entries) != 3 {
		t.Fatal("DDL fixture lacks retained committed baseline")
	}
	rows := s.Transactions.Writes[d.SQLIndex.Parent]
	members, nonmembers := 0, 0
	for _, row := range rows {
		output, ok := row.IndexOutputs[built.Address()]
		if !ok || output.Index != built {
			t.Fatal("missing DDL-produced output")
		}
		if index.RowMaintainsIndex(row, built) {
			t.Fatal("fixture admission unexpectedly includes later index")
		}
		if output.Member {
			members++
		} else {
			nonmembers++
			if len(output.Keys) != 0 {
				t.Fatal("nonmember keys")
			}
		}
	}
	if members != 2 || nonmembers != 1 {
		t.Fatal("DDL member/nonmember witness", members, nonmembers)
	}
	if err := amcheckSessionCall(t, e, s, "SELECT bt_index_parent_check('ddl_i',true,true,true)"); err != nil {
		t.Fatal(err)
	}
	if e.Storage.IndexRoots[d.Address()] != committed || len(committed.Entries) != 5 {
		t.Fatal("private build changed committed root")
	}
	if index.EffectiveRoot(e.Storage.IndexRoots, s.Transactions.Catalog, built.Address()) != baseline || len(baseline.Entries) != 3 {
		t.Fatal("verification replaced DDL baseline authority")
	}
}

func TestAmcheckPrivateEquivalentRepresentation(t *testing.T) {
	e := engine.New()
	amcheckUnitInstalled(t, e)
	enginetest.Run(t, e, "CREATE TABLE numeric_private(n NUMERIC)")
	enginetest.Run(t, e, "INSERT INTO numeric_private VALUES(2.0)")
	s := sessionStart(t, e, nil)
	tx := transactionBegin(t, s)
	defer transactionEnd(t, s, tx, false)
	sessionSQL(t, s, "INSERT INTO numeric_private VALUES(1.0)")
	sessionSQL(t, s, "CREATE INDEX numeric_private_i ON numeric_private(n)")
	var target *catalog.Relation
	var address ids.ObjectAddress
	for parent, rows := range s.Transactions.Writes {
		for _, row := range rows {
			for _, output := range row.IndexOutputs {
				if output.Index.Name() == "numeric_private_i" {
					target = output.Index
					address = parent
				}
			}
		}
	}
	if target == nil {
		t.Fatal("no genuine private DDL output")
	}
	original := s.Transactions.Writes[address]
	rows := append([]storage.RowVersion(nil), original...)
	output := rows[0].IndexOutputs[target.Address()]
	equivalent, err := output.Keys[0].Type().Input(context.Background(), "1.00", -1)
	if err != nil || sqltype.SameDatum(equivalent, output.Keys[0]) {
		t.Fatal("representation fixture", err)
	}
	order, err := sqltype.BindOrdering(equivalent.Type().Usage())
	if err != nil {
		t.Fatal(err)
	}
	if cmp, err := order.Compare(context.Background(), equivalent, output.Keys[0], sqltype.InputEnvironment{}); err != nil || cmp != 0 {
		t.Fatal("not comparator-equivalent", cmp, err)
	}
	output.Keys = []sqltype.Value{equivalent}
	rows[0].IndexOutputs = maps.Clone(rows[0].IndexOutputs)
	rows[0].IndexOutputs[target.Address()] = output
	s.Transactions.Writes = maps.Clone(s.Transactions.Writes)
	s.Transactions.Writes[address] = rows
	if err := amcheckSessionCall(t, e, s, "SELECT bt_index_check('numeric_private_i',false)"); err != nil {
		t.Fatal(err)
	}
	version := uint64(rows[0].Version)
	message := fmt.Sprintf("heap tuple (%d,%d) from table \"numeric_private\" lacks matching index tuple within index \"numeric_private_i\"", (version-1)/65535, (version-1)%65535+1)
	amcheckUnitDiagnostic(t, amcheckSessionCall(t, e, s, "SELECT bt_index_parent_check('numeric_private_i',true)"), sqlstate.Error{Code: "XX001", Message: message})
	if !sqltype.SameDatum(original[0].IndexOutputs[target.Address()].Keys[0], original[0].Values[0]) {
		t.Fatal("checker changed original output")
	}
}
