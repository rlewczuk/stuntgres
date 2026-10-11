package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceControlTransactionsReplacement(t *testing.T) {
	for _, restore := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			name := map[bool]string{false: "reset", true: "restore"}[restore] + "/" + map[bool]string{false: "direct", true: "prepared"}[prepared]
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				p := fixture.NewScenario(t)
				fixture.CatalogControlSQLWitness(t, p)
				d := p.NativeConnector.Driver().(*stuntgres.Driver)
				db := fixture.CatalogControlPool(t, d, "reference/"+t.Name())
				peer := fixture.CatalogControlPool(t, d, "reference/"+t.Name())
				isolated, isolatedTx := catalogControlIsolatedTransactions(t, ctx, d)
				fixture.CatalogControlAffected(t, ctx, peer, "CREATE TABLE t(v INT NOT NULL)", false, 0)
				fixture.CatalogControlAffected(t, ctx, peer, "INSERT INTO t VALUES(77)", false, 1)
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
				fixture.CatalogControlAffected(t, ctx, b, "SET application_name TO 'peer'", false, 0)
				fixture.CatalogControlAffected(t, ctx, a, "SET application_name TO 'base'", false, 0)
				fixture.CatalogControlAffected(t, ctx, a, "BEGIN", false, 0)
				fixture.CatalogControlAffected(t, ctx, a, "SET application_name TO 'candidate'", false, 0)
				fixture.CatalogControlAffected(t, ctx, a, "SET LOCAL application_name TO 'local'", false, 0)
				fixture.CatalogControlAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
				retainedSetting, err := a.QueryContext(ctx, "SHOW application_name")
				if err != nil {
					t.Fatal(err)
				}
				defer retainedSetting.Close()
				fixture.CatalogControlSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 77)
				fixture.CatalogControlSelect(t, ctx, peer, "SELECT v FROM t", false, 77)
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
				clone := fixture.CatalogControlPool(t, stuntgres.NewDriver(), t.Name()+"/clone")
				if err = stuntgres.Restore(ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				fixture.CatalogControlSelect(t, ctx, clone, "SELECT v FROM t", false, 77)
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
				fixture.CatalogControlNativeValues(t, rows, 77)
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
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				}
				_, err = end.ExecContext(ctx)
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				var setting string
				if err = a.QueryRowContext(ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "base" {
					t.Fatal(setting, err)
				}
				catalogControlFreshReplacement(t, ctx, peer, restore)
				fixture.CatalogControlAffected(t, ctx, a, "INSERT INTO t VALUES('2')", false, 1)
				fixture.CatalogControlAffected(t, ctx, a, "BEGIN", false, 0)
				if r, err := end.ExecContext(ctx); err != nil {
					t.Fatal("retained end lost", err)
				} else if n, _ := r.RowsAffected(); n != 0 {
					t.Fatal(n)
				}
				fixture.CatalogControlAffected(t, ctx, a, "ROLLBACK", false, 0)
				for i, x := range isolated {
					fixture.CatalogControlSelect(t, ctx, x, "SELECT v FROM t", false, []int64{22, 33}[i])
					if err = isolatedTx[i].Commit(); err != nil {
						t.Fatal("isolated transaction fenced", err)
					}
					fixture.CatalogControlSelect(t, ctx, x, "SELECT v FROM t ORDER BY v", false, []int64{22, 33}[i], 44)
				}
				a.Close()
				db.Close()
				reopened := fixture.CatalogControlPool(t, d, "reference/"+t.Name())
				fixture.CatalogControlTuples(t, reopened, "SELECT replacement FROM t ORDER BY replacement", map[bool][][]any{false: {{"2"}}, true: {{"2"}, {"99"}}}[restore])
			})
		}
	}
}

func catalogControlIsolatedTransactions(t *testing.T, ctx context.Context, d *stuntgres.Driver) ([]*sql.DB, []*sql.Tx) {
	t.Helper()
	isolated := []*sql.DB{fixture.CatalogControlPool(t, d, "reference/"+t.Name()+" "), fixture.CatalogControlPool(t, stuntgres.NewDriver(), "reference/"+t.Name())}
	var transactions []*sql.Tx
	for i, x := range isolated {
		fixture.CatalogControlAffected(t, ctx, x, "CREATE TABLE t(v INT)", false, 0)
		fixture.CatalogControlAffected(t, ctx, x, []string{"INSERT INTO t VALUES(22)", "INSERT INTO t VALUES(33)"}[i], false, 1)
		tx, err := x.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		fixture.CatalogControlAffected(t, ctx, tx, "INSERT INTO t VALUES(44)", false, 1)
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
		want := fixture.ControlsReferenceDiagnostic("42P01", `relation "t" does not exist`)
		want.Position = "15"
		fixture.AssertControlDiagnostic(t, err, want)
		fixture.CatalogControlAffected(t, ctx, peer, "CREATE TABLE t(replacement TEXT)", false, 0)
	}
}
