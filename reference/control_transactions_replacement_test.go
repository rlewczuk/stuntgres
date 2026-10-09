package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceControlTransactionsReplacement(t *testing.T) {
	for _, restore := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			name := map[bool]string{false: "reset", true: "restore"}[restore] + "/" + map[bool]string{false: "direct", true: "prepared"}[prepared]
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				p := newReferenceScenario(t)
				catalogControlSQLWitness(t, p)
				d := p.nativeConnector.Driver().(*stuntgres.Driver)
				db := catalogControlPool(t, d, "reference/"+t.Name())
				peer := catalogControlPool(t, d, "reference/"+t.Name())
				isolated, isolatedTx := catalogControlIsolatedTransactions(t, ctx, d)
				catalogControlAffected(t, ctx, peer, "CREATE TABLE t(v INT NOT NULL)", false, 0)
				catalogControlAffected(t, ctx, peer, "INSERT INTO t VALUES(77)", false, 1)
				a, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
				defer a.ExecContext(context.Background(), "ROLLBACK")
				b, err := peer.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close()
				catalogControlAffected(t, ctx, b, "SET application_name TO 'peer'", false, 0)
				catalogControlAffected(t, ctx, a, "SET application_name TO 'base'", false, 0)
				catalogControlAffected(t, ctx, a, "BEGIN", false, 0)
				catalogControlAffected(t, ctx, a, "SET application_name TO 'candidate'", false, 0)
				catalogControlAffected(t, ctx, a, "SET LOCAL application_name TO 'local'", false, 0)
				catalogControlAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
				retainedSetting, err := a.QueryContext(ctx, "SHOW application_name")
				if err != nil {
					t.Fatal(err)
				}
				defer retainedSetting.Close()
				catalogControlSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 77)
				catalogControlSelect(t, ctx, peer, "SELECT v FROM t", false, 77)
				var ordinary *sql.Stmt
				if prepared {
					ordinary, err = a.PrepareContext(ctx, "SELECT v FROM t")
					if err != nil {
						t.Fatal(err)
					}
					defer ordinary.Close()
				}
				end, err := a.PrepareContext(ctx, "COMMIT AND CHAIN")
				if err != nil {
					t.Fatal(err)
				}
				defer end.Close()
				rows, err := peer.QueryContext(ctx, "SELECT v FROM t")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var dump bytes.Buffer
				if err = stuntgres.Dump(ctx, peer, &dump); err != nil {
					t.Fatal(err)
				}
				clone := catalogControlPool(t, stuntgres.NewDriver(), t.Name()+"/clone")
				if err = stuntgres.Restore(ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				catalogControlSelect(t, ctx, clone, "SELECT v FROM t", false, 77)
				if restore {
					err = stuntgres.Restore(ctx, peer, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('99');"))
				} else {
					err = stuntgres.Reset(ctx, peer)
				}
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].Name() != "v" || types[0].DatabaseTypeName() != "INT4" {
					t.Fatal("retained metadata", types, err)
				}
				catalogControlNativeValues(t, rows, 77)
				if !retainedSetting.Next() {
					t.Fatal("missing retained setting", retainedSetting.Err())
				}
				var retainedName string
				if err = retainedSetting.Scan(&retainedName); err != nil || retainedName != "local" {
					t.Fatal(retainedName, err)
				}
				if retainedSetting.Next() || retainedSetting.Err() != nil {
					t.Fatal("retained setting changed", retainedSetting.Err())
				}
				retainedSetting.Close()
				var peerName string
				if err = b.QueryRowContext(ctx, "SHOW application_name").Scan(&peerName); err != nil || peerName != "peer" {
					t.Fatal(peerName, err)
				}
				// database/sql closes its Rows metadata API; captured descriptors
				// remain independently usable after the cursor has been closed.
				if types[0].Name() != "v" || types[0].DatabaseTypeName() != "INT4" {
					t.Fatal("captured metadata changed after Close", types)
				}
				for range 2 {
					if prepared {
						var r *sql.Rows
						r, err = ordinary.QueryContext(ctx)
						if r != nil {
							r.Close()
						}
					} else {
						_, err = a.ExecContext(ctx, "SELECT v FROM t")
					}
					transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				}
				_, err = end.ExecContext(ctx)
				transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				var setting string
				if err = a.QueryRowContext(ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "base" {
					t.Fatal(setting, err)
				}
				catalogControlFreshReplacement(t, ctx, peer, restore)
				catalogControlAffected(t, ctx, a, "INSERT INTO t VALUES('2')", false, 1)
				catalogControlAffected(t, ctx, a, "BEGIN", false, 0)
				if r, err := end.ExecContext(ctx); err != nil {
					t.Fatal("retained end lost", err)
				} else if n, _ := r.RowsAffected(); n != 0 {
					t.Fatal(n)
				}
				catalogControlAffected(t, ctx, a, "ROLLBACK", false, 0)
				for i, x := range isolated {
					catalogControlSelect(t, ctx, x, "SELECT v FROM t", false, []int64{22, 33}[i])
					if err = isolatedTx[i].Commit(); err != nil {
						t.Fatal("isolated transaction fenced", err)
					}
					catalogControlSelect(t, ctx, x, "SELECT v FROM t ORDER BY v", false, []int64{22, 33}[i], 44)
				}
				a.Close()
				db.Close()
				reopened := catalogControlPool(t, d, "reference/"+t.Name())
				catalogControlTuples(t, reopened, "SELECT replacement FROM t ORDER BY replacement", map[bool][][]any{false: {{"2"}}, true: {{"2"}, {"99"}}}[restore])
			})
		}
	}
}

func catalogControlIsolatedTransactions(t *testing.T, ctx context.Context, d *stuntgres.Driver) ([]*sql.DB, []*sql.Tx) {
	t.Helper()
	isolated := []*sql.DB{catalogControlPool(t, d, "reference/"+t.Name()+" "), catalogControlPool(t, stuntgres.NewDriver(), "reference/"+t.Name())}
	var transactions []*sql.Tx
	for i, x := range isolated {
		catalogControlAffected(t, ctx, x, "CREATE TABLE t(v INT)", false, 0)
		catalogControlAffected(t, ctx, x, []string{"INSERT INTO t VALUES(22)", "INSERT INTO t VALUES(33)"}[i], false, 1)
		tx, err := x.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		catalogControlAffected(t, ctx, tx, "INSERT INTO t VALUES(44)", false, 1)
		transactions = append(transactions, tx)
	}
	return isolated, transactions
}
func catalogControlFreshReplacement(t *testing.T, ctx context.Context, peer *sql.DB, restore bool) {
	t.Helper()
	var err error
	if restore {
		fresh, err := peer.QueryContext(ctx, "SELECT replacement FROM t")
		if err != nil {
			t.Fatal(err)
		}
		ct, err := fresh.ColumnTypes()
		if err != nil || len(ct) != 1 || ct[0].DatabaseTypeName() != "TEXT" {
			t.Fatal(ct, err)
		}
		if !fresh.Next() {
			t.Fatal("missing replacement", fresh.Err())
		}
		var v string
		if err = fresh.Scan(&v); err != nil || v != "99" {
			t.Fatal(v, err)
		}
		if fresh.Next() || fresh.Err() != nil {
			t.Fatal("extra replacement", fresh.Err())
		}
		fresh.Close()
	} else {
		_, err = peer.QueryContext(ctx, "SELECT v FROM t")
		want := controlsReferenceDiagnostic("42P01", `relation "t" does not exist`)
		want.Position = "15"
		referenceAssertControlDiagnostic(t, err, want)
		catalogControlAffected(t, ctx, peer, "CREATE TABLE t(replacement TEXT)", false, 0)
	}
}
