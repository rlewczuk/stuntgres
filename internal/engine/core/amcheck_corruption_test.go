package core_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func amcheckUnitRoot(t *testing.T, unique bool) (*core.Engine, *catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	e := engine.New()
	amcheckUnitInstalled(t, e)
	enginetest.Run(t, e, "CREATE TABLE t(n INT,s TEXT)")
	if unique {
		enginetest.Run(t, e, "INSERT INTO t VALUES(1,'a'),(2,'b'),(NULL,NULL)")
	} else {
		enginetest.Run(t, e, "INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')")
	}
	prefix := "CREATE INDEX"
	if unique {
		prefix = "CREATE UNIQUE INDEX"
	}
	enginetest.Run(t, e, prefix+" i ON t(n)")
	d, root := btreeRoot(t, e, "i")
	return e, d, root
}
func amcheckUnitCall(t *testing.T, e *core.Engine, query string) error {
	t.Helper()
	result, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, query), nil)
	if err == nil && result.Rows != nil {
		result.Rows.Close()
	}
	return err
}
func amcheckUnitDiagnostic(t *testing.T, err error, want sqlstate.Error) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		t.Fatalf("missing structured diagnostic: %v", err)
	}
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	if !reflect.DeepEqual(*diagnostic, want) {
		t.Fatalf("diagnostic\n got: %#v\nwant: %#v", *diagnostic, want)
	}
}
func amcheckUnitClone(root *storage.IndexRoot) *storage.IndexRoot {
	next := *root
	next.Entries = append([]storage.IndexEntry(nil), root.Entries...)
	return &next
}

func TestAmcheckLogicalCorruption(t *testing.T) {
	for _, damage := range []string{"order", "arity", "position", "identity", "duplicate", "membership", "ordinal"} {
		t.Run(damage, func(t *testing.T) {
			e, d, root := amcheckUnitRoot(t, false)
			before := btreeKeys(t, root)
			if err := amcheckUnitCall(t, e, "SELECT bt_index_parent_check('i',true,true,true)"); err != nil {
				t.Fatal(err)
			}
			next := amcheckUnitClone(root)
			switch damage {
			case "order":
				next.Entries[0], next.Entries[1] = next.Entries[1], next.Entries[0]
			case "arity":
				next.Entries[0].Keys = nil
			case "position":
				next.Entries[0].Position = len(e.Storage.Relations[d.SQLIndex.Parent].Versions)
			case "identity":
				next.Entries[0].Version++
			case "duplicate":
				next.Entries[1] = next.Entries[0]
			case "membership":
				next.Membership = map[ids.RowID]ids.VersionID{next.Entries[0].Row: next.Entries[0].Version}
			case "ordinal":
				next.RowEntries = map[ids.RowID]int{next.Entries[0].Row: 99}
			}
			e.Storage.IndexRoots[d.Address()] = next
			amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT bt_index_parent_check('i',false,true,true)"), sqlstate.Error{Code: "XX002", Message: `item order invariant violated for index "i"`})
			if !reflect.DeepEqual(btreeKeys(t, root), before) {
				t.Fatal("checker modified original root")
			}
			e.Storage.IndexRoots[d.Address()] = root
			if err := amcheckUnitCall(t, e, "SELECT bt_index_parent_check('i',true,true,true)"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestAmcheckHeapRecipes(t *testing.T) {
	e, d, root := amcheckUnitRoot(t, false)
	missing := root.Entries[0]
	next := amcheckUnitClone(root)
	next.Entries = next.Entries[1:]
	e.Storage.IndexRoots[d.Address()] = next
	if err := amcheckUnitCall(t, e, "SELECT bt_index_check('i',false)"); err != nil {
		t.Fatal(err)
	}
	block, offset, err := exec.PgrowlocksLogicalTID(missing.Version)
	if err != nil {
		t.Fatal(err)
	}
	message := fmt.Sprintf("heap tuple (%d,%d) from table \"t\" lacks matching index tuple within index \"i\"", block, offset)
	amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT bt_index_check('i',true)"), sqlstate.Error{Code: "XX001", Message: message, Hint: "Retrying verification using the function bt_index_parent_check() might provide a more specific error."})
	amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT bt_index_parent_check('i',true)"), sqlstate.Error{Code: "XX001", Message: message})
	e.Storage.IndexRoots[d.Address()] = root
	enginetest.Run(t, e, "CREATE INDEX expression_i ON t(lower(s)) WHERE n>1")
	definition, expressionRoot := btreeRoot(t, e, "expression_i")
	if err := amcheckUnitCall(t, e, "SELECT bt_index_parent_check('expression_i',true,true)"); err != nil {
		t.Fatal(err)
	}
	damaged := amcheckUnitClone(expressionRoot)
	damaged.Entries = damaged.Entries[1:]
	damaged.Membership = nil
	e.Storage.IndexRoots[definition.Address()] = damaged
	block, offset, err = exec.PgrowlocksLogicalTID(expressionRoot.Entries[0].Version)
	if err != nil {
		t.Fatal(err)
	}
	amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT bt_index_check('expression_i',true)"), sqlstate.Error{Code: "XX001", Message: fmt.Sprintf("heap tuple (%d,%d) from table \"t\" lacks matching index tuple within index \"expression_i\"", block, offset), Hint: "Retrying verification using the function bt_index_parent_check() might provide a more specific error."})
	if len(root.Entries) != 5 || len(expressionRoot.Entries) != 3 {
		t.Fatal("mutated retained roots")
	}
	e.Storage.IndexRoots[definition.Address()] = expressionRoot
	enginetest.Run(t, e, "CREATE TABLE representation_t(n NUMERIC)")
	enginetest.Run(t, e, "INSERT INTO representation_t VALUES(1.0)")
	enginetest.Run(t, e, "CREATE INDEX representation_i ON representation_t(n)")
	representation, represented := btreeRoot(t, e, "representation_i")
	equivalent, err := represented.Entries[0].Keys[0].Type().Input(context.Background(), "1.00", -1)
	if err != nil || sqltype.SameDatum(equivalent, represented.Entries[0].Keys[0]) {
		t.Fatal("fixture lost distinct equivalent representation", err)
	}
	wrongRepresentation := amcheckUnitClone(represented)
	wrongRepresentation.Entries[0].Keys = []sqltype.Value{equivalent}
	e.Storage.IndexRoots[representation.Address()] = wrongRepresentation
	if err := amcheckUnitCall(t, e, "SELECT bt_index_check('representation_i',false)"); err != nil {
		t.Fatal(err)
	}
	block, offset, err = exec.PgrowlocksLogicalTID(represented.Entries[0].Version)
	if err != nil {
		t.Fatal(err)
	}
	amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT bt_index_parent_check('representation_i',true)"), sqlstate.Error{Code: "XX001", Message: fmt.Sprintf("heap tuple (%d,%d) from table \"representation_t\" lacks matching index tuple within index \"representation_i\"", block, offset)})
}
func TestAmcheckUniqueVisibility(t *testing.T) {
	e, d, root := amcheckUnitRoot(t, true)
	next := amcheckUnitClone(root)
	next.Entries[1].Keys = next.Entries[0].Keys
	e.Storage.IndexRoots[d.Address()] = next
	if err := amcheckUnitCall(t, e, "SELECT bt_index_check('i',false,false)"); err != nil {
		t.Fatal(err)
	}
	amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT bt_index_check('i',false,true)"), sqlstate.Error{Code: "XX002", Message: `index uniqueness is violated for index "i"`})
	nulls := amcheckUnitClone(root)
	for i := range nulls.Entries {
		nulls.Entries[i].Keys = []sqltype.Value{sqltype.Null(root.Entries[0].Keys[0].Type())}
	}
	e.Storage.IndexRoots[d.Address()] = nulls
	if err := amcheckUnitCall(t, e, "SELECT bt_index_check('i',false,true)"); err != nil {
		t.Fatal("REL18 NULL-key uniqueness exemption", err)
	}
	e.Storage.IndexRoots[d.Address()] = root
	enginetest.Run(t, e, "DELETE FROM t WHERE n=2")
	// DELETE retains the actual row version/end marker, but no longer demands
	// its old entry for visible uniqueness. Inject the retained old vector only.
	e.Storage.IndexRoots[d.Address()] = next
	if err := amcheckUnitCall(t, e, "SELECT bt_index_check('i',false,true)"); err != nil {
		t.Fatal("dead version uniqueness", err)
	}
	if got := btreeKeys(t, root); !reflect.DeepEqual(got, []string{"1", "2", "NULL"}) {
		t.Fatal(got)
	}
	e.Storage.IndexRoots[d.Address()] = root
	enginetest.Run(t, e, "CREATE UNIQUE INDEX i_nnd ON t(n) NULLS NOT DISTINCT")
	nnd, nndRoot := btreeRoot(t, e, "i_nnd")
	if !nnd.SQLIndex.NullsNotDistinct {
		t.Fatal("fixture is not a real NND index")
	}
	nndNulls := amcheckUnitClone(nndRoot)
	for i := range nndNulls.Entries {
		nndNulls.Entries[i].Keys = []sqltype.Value{sqltype.Null(nndRoot.Entries[i].Keys[0].Type())}
	}
	e.Storage.IndexRoots[nnd.Address()] = nndNulls
	if err := amcheckUnitCall(t, e, "SELECT bt_index_check('i_nnd',false,true)"); err != nil {
		t.Fatal("REL18 NND NULL-key exemption", err)
	}
}
func TestAmcheckRootDescent(t *testing.T) {
	e, _, _ := amcheckUnitRoot(t, false)
	enginetest.Run(t, e, "CREATE INDEX descending_i ON t(n DESC NULLS FIRST,s)")
	if err := amcheckUnitCall(t, e, "SELECT bt_index_parent_check('descending_i',true,true,true)"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d, root := btreeRoot(t, e, "descending_i")
	parent := e.Catalog.State.MustRelation(d.SQLIndex.Parent)
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: e.CatalogRead()}}
	capture := index.AmcheckCapture{Index: d, Parent: parent, Root: root, Store: *e.Storage.Relations[parent.Address()], Frontier: e.Visibility.CommitSeq()}
	if err := index.CheckBtree(&evaluation, capture, index.AmcheckOptions{Parent: true, RootDescend: true}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAmcheckBaselineCorruptionWithPrivateRows(t *testing.T) {
	for _, damage := range []string{"order", "missing", "unique"} {
		t.Run(damage, func(t *testing.T) {
			e, d, root := amcheckUnitRoot(t, true)
			s := sessionStart(t, e, nil)
			tx := transactionBegin(t, s)
			defer transactionEnd(t, s, tx, false)
			sessionSQL(t, s, "INSERT INTO t VALUES(3,'c'),(4,'d')")
			if err := amcheckSessionCall(t, e, s, "SELECT bt_index_parent_check('i',true,true,true)"); err != nil {
				t.Fatal(err)
			}
			next := amcheckUnitClone(root)
			want := sqlstate.Error{Code: "XX002", Message: `item order invariant violated for index "i"`}
			switch damage {
			case "order":
				next.Entries[0], next.Entries[1] = next.Entries[1], next.Entries[0]
			case "unique":
				next.Entries[1].Keys = next.Entries[0].Keys
				want.Message = `index uniqueness is violated for index "i"`
			case "missing":
				version := uint64(next.Entries[0].Version)
				next.Entries = next.Entries[1:]
				next.Membership = nil
				next.RowEntries = nil
				want = sqlstate.Error{Code: "XX001", Message: fmt.Sprintf("heap tuple (%d,%d) from table \"t\" lacks matching index tuple within index \"i\"", (version-1)/65535, (version-1)%65535+1)}
			}
			e.Storage.IndexRoots[d.Address()] = next
			query := "SELECT bt_index_parent_check('i',true,true,true)"
			if damage == "unique" {
				query = "SELECT bt_index_parent_check('i',false,true,true)"
			}
			rows := s.Transactions.Writes[d.SQLIndex.Parent]
			beforeRows := append([]storage.RowVersion(nil), rows...)
			for i := range beforeRows {
				beforeRows[i].Values = append([]sqltype.Value(nil), rows[i].Values...)
				beforeRows[i].IndexOutputs = maps.Clone(rows[i].IndexOutputs)
				for address, output := range beforeRows[i].IndexOutputs {
					output.Keys = append([]sqltype.Value(nil), output.Keys...)
					beforeRows[i].IndexOutputs[address] = output
				}
			}
			beforeKeys := btreeKeys(t, root)
			beforeDamaged := amcheckUnitClone(next)
			beforeDamaged.Membership = maps.Clone(next.Membership)
			beforeDamaged.RowEntries = maps.Clone(next.RowEntries)
			for i := range beforeDamaged.Entries {
				beforeDamaged.Entries[i].Keys = append([]sqltype.Value(nil), next.Entries[i].Keys...)
			}
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			result, verifyErr := scope.Execute(enginetest.Statement(t, query), 0, nil)
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("corrupt verification returned rows")
			}
			amcheckUnitDiagnostic(t, verifyErr, want)
			// Verify immutability before ordinary failed-statement abort disposal.
			if len(rows) != 2 || !reflect.DeepEqual(s.Transactions.Writes[d.SQLIndex.Parent], beforeRows) || !reflect.DeepEqual(btreeKeys(t, root), beforeKeys) || !reflect.DeepEqual(next, beforeDamaged) || e.Storage.IndexRoots[d.Address()] != next {
				t.Fatal("verification changed genuine private or baseline authority")
			}
			finishErr := scope.Finish(verifyErr)
			amcheckUnitDiagnostic(t, finishErr, want)
			op.Done()
			if s.TransactionState() != session.TxAborted || len(s.Transactions.Writes) != 0 || len(s.Transactions.Deletes) != 0 || len(s.Transactions.Roots) != 0 {
				t.Fatal("failed statement did not dispose normal aborted transaction state")
			}
			if !reflect.DeepEqual(rows, beforeRows) || !reflect.DeepEqual(btreeKeys(t, root), beforeKeys) || !reflect.DeepEqual(next, beforeDamaged) {
				t.Fatal("abort disposal mutated retained captures")
			}
		})
	}
}

func TestAmcheckKnownLogicalTID(t *testing.T) {
	e := engine.New()
	amcheckUnitInstalled(t, e)
	enginetest.Run(t, e, "CREATE TABLE tid_known(n INT)")
	// Exercise a block boundary through the actual allocator and genuine DML,
	// not the production coordinate converter used to form the diagnostic.
	e.Mu.Lock()
	e.Storage.SetFrontierLocked(65535, 65535)
	e.Mu.Unlock()
	enginetest.Run(t, e, "INSERT INTO tid_known VALUES(7)")
	enginetest.Run(t, e, "CREATE INDEX tid_known_i ON tid_known(n)")
	d, root := btreeRoot(t, e, "tid_known_i")
	if len(root.Entries) != 1 || root.Entries[0].Version != 65536 {
		t.Fatal("known version fixture", root.Entries)
	}
	next := amcheckUnitClone(root)
	next.Entries = nil
	next.Membership = nil
	next.RowEntries = nil
	e.Storage.IndexRoots[d.Address()] = next
	// 65536 - 1 = 1*65535 + 0, hence block 1, one-based offset 1.
	amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT bt_index_parent_check('tid_known_i',true)"), sqlstate.Error{Code: "XX001", Message: `heap tuple (1,1) from table "tid_known" lacks matching index tuple within index "tid_known_i"`})
	if len(root.Entries) != 1 || root.Entries[0].Version != 65536 {
		t.Fatal("checker mutated known retained entry")
	}
}
