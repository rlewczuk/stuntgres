package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
)

func TestControlTransactionsReplacement(t *testing.T) {
	for _, restore := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			name := map[bool]string{false: "reset", true: "restore"}[restore] + "/" + map[bool]string{false: "direct", true: "prepared"}[prepared]
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				d := stuntgres.NewDriver()
				db := openLocal(t, d, t.Name())
				peer := openLocal(t, d, t.Name())
				isolated := []*sql.DB{openLocal(t, d, t.Name()+" "), openLocal(t, stuntgres.NewDriver(), t.Name())}
				var isolatedTx []*sql.Tx
				for i, x := range isolated {
					transactionAffected(t, ctx, x, "CREATE TABLE t(v INT)", false, 0)
					transactionAffected(t, ctx, x, []string{"INSERT INTO t VALUES(22)", "INSERT INTO t VALUES(33)"}[i], false, 1)
					tx, err := x.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(44)", false, 1)
					isolatedTx = append(isolatedTx, tx)
				}
				transactionAffected(t, ctx, peer, "CREATE TABLE t(v INT NOT NULL)", false, 0)
				transactionAffected(t, ctx, peer, "INSERT INTO t VALUES(77)", false, 1)
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
				transactionAffected(t, ctx, b, "SET application_name TO 'peer'", false, 0)
				transactionAffected(t, ctx, a, "SET application_name TO 'base'", false, 0)
				transactionAffected(t, ctx, a, "BEGIN", false, 0)
				transactionAffected(t, ctx, a, "SET application_name TO 'candidate'", false, 0)
				transactionAffected(t, ctx, a, "SET LOCAL application_name TO 'local'", false, 0)
				transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
				retainedSetting, err := a.QueryContext(ctx, "SHOW application_name")
				if err != nil {
					t.Fatal(err)
				}
				defer retainedSetting.Close()
				transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 77)
				transactionSelect(t, ctx, peer, "SELECT v FROM t", false, 77)
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
				clone := openLocal(t, stuntgres.NewDriver(), t.Name()+"/clone")
				if err = stuntgres.Restore(ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				transactionSelect(t, ctx, clone, "SELECT v FROM t", false, 77)
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
				transactionValues(t, rows, 77)
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
					want := controlsDiagnostic("42P01", `relation "t" does not exist`)
					want.Position = "15"
					transactionDiagnostic(t, err, want)
					transactionAffected(t, ctx, peer, "CREATE TABLE t(replacement TEXT)", false, 0)
				}
				transactionAffected(t, ctx, a, "INSERT INTO t VALUES('2')", false, 1)
				transactionAffected(t, ctx, a, "BEGIN", false, 0)
				if r, err := end.ExecContext(ctx); err != nil {
					t.Fatal("retained end lost", err)
				} else if n, _ := r.RowsAffected(); n != 0 {
					t.Fatal(n)
				}
				transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
				for i, x := range isolated {
					transactionSelect(t, ctx, x, "SELECT v FROM t", false, []int64{22, 33}[i])
					if err = isolatedTx[i].Commit(); err != nil {
						t.Fatal("isolated transaction fenced", err)
					}
					transactionSelect(t, ctx, x, "SELECT v FROM t ORDER BY v", false, []int64{22, 33}[i], 44)
				}
				a.Close()
				db.Close()
				reopened := openLocal(t, d, t.Name())
				wantTuples(t, reopened, "SELECT replacement FROM t ORDER BY replacement", map[bool][][]any{false: {{"2"}}, true: {{"2"}, {"99"}}}[restore])
			})
		}
	}
}

func TestControlTransactionsFailedRestoreEligibility(t *testing.T) {
	for _, failure := range []string{"syntax", "missing", "control", "reader", "precancel", "readcancel", "aborted"} {
		t.Run(failure, func(t *testing.T) {
			db, a, b, _ := controlsFixture(t)
			ctx := context.Background()
			transactionAffected(t, ctx, b, "INSERT INTO t VALUES(77)", false, 1)
			transactionAffected(t, ctx, a, "BEGIN", false, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
			transactionAffected(t, ctx, a, "SET application_name TO 'candidate'", false, 0)
			rows, err := b.QueryContext(ctx, "SELECT v FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			switch failure {
			case "reader":
				want := errors.New("read failed")
				err = stuntgres.Restore(ctx, db, failingReader{want})
				if !errors.Is(err, want) {
					t.Fatal(err)
				}
			case "precancel", "readcancel":
				c, cancel := context.WithCancel(ctx)
				defer cancel()
				if failure == "precancel" {
					cancel()
					err = stuntgres.Restore(c, db, strings.NewReader("CREATE TABLE x(v INT)"))
				} else {
					err = stuntgres.Restore(c, db, &cancelReader{cancel: cancel})
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			default:
				if failure == "aborted" {
					transactionDiagnostic(t, transactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(NULL)", false), controlsNull())
				}
				scripts := map[string]string{"syntax": "CREATE TABLE x(v INT); INSERT INTO x VALUES(;", "missing": "CREATE TABLE x(v INT); INSERT INTO missing VALUES(9);", "control": "BEGIN;", "aborted": "BEGIN;"}
				err = stuntgres.Restore(ctx, db, strings.NewReader(scripts[failure]))
				sqlState(t, err, map[string]string{"syntax": "42601", "missing": "42P01", "control": "0A000", "aborted": "0A000"}[failure])
			}
			transactionValues(t, rows, 77)
			transactionSelect(t, ctx, b, "SELECT v FROM t", false, 77)
			if failure == "aborted" {
				_, err = a.ExecContext(ctx, "SELECT v FROM t")
				transactionDiagnostic(t, err, controlsAborted())
				transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
			} else {
				transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 77)
				transactionAffected(t, ctx, a, "COMMIT", false, 0)
				transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 77)
				var name string
				if err = a.QueryRowContext(ctx, "SHOW application_name").Scan(&name); err != nil || name != "candidate" {
					t.Fatal(name, err)
				}
			}
		})
	}
}

func TestControlTransactionsGatedIO(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "install", true: "cancel"}[canceled], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			d := stuntgres.NewDriver()
			db, peer := openLocal(t, d, t.Name()), openLocal(t, d, t.Name())
			peer.SetMaxOpenConns(1)
			transactionAffected(t, ctx, peer, "CREATE TABLE t(v INT)", false, 0)
			transactionAffected(t, ctx, peer, "INSERT INTO t VALUES(77)", false, 1)
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			defer a.ExecContext(context.Background(), "ROLLBACK")
			transactionAffected(t, ctx, a, "BEGIN", false, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
			r := &gatedReader{started: make(chan struct{}), release: make(chan struct{})}
			c, stop := context.WithCancel(ctx)
			defer stop()
			done := make(chan error, 1)
			go func() { done <- stuntgres.Restore(c, peer, r) }()
			released, joined := false, false
			defer func() {
				if !released {
					close(r.release)
				}
				if !joined {
					<-done
				}
			}()
			select {
			case <-r.started:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			transactionSelect(t, ctx, peer, "SELECT v FROM t", false, 77)
			transactionAffected(t, ctx, peer, "INSERT INTO t VALUES(88)", false, 1)
			transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 77, 88)
			if canceled {
				stop()
			}
			close(r.release)
			released = true
			err = <-done
			joined = true
			if canceled {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				transactionAffected(t, ctx, a, "COMMIT", false, 0)
				transactionSelect(t, ctx, peer, "SELECT v FROM t ORDER BY v", false, 1, 77, 88)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				_, err = a.ExecContext(ctx, "SELECT v FROM t")
				transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				transactionSelect(t, ctx, peer, "SELECT v FROM replaced", false, 99)
			}
		})
	}
}

func TestControlTransactionsGoCommitAndRollbackRecovery(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "go_commit", true: "rollback_chain"}[rollback], func(t *testing.T) {
			db, a, b, _ := controlsFixture(t)
			ctx := context.Background()
			transactionAffected(t, ctx, b, "INSERT INTO t VALUES(77)", false, 1)
			if rollback {
				transactionAffected(t, ctx, a, "BEGIN", false, 0)
				transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
			} else {
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(1)", false, 1)
				if err = stuntgres.Reset(ctx, db); err != nil {
					t.Fatal(err)
				}
				_, err = tx.ExecContext(ctx, "SELECT v FROM t")
				transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				transactionOrdinary(t, tx.Commit(), "stuntgres: catalog replaced before publication")
				transactionAffected(t, ctx, b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
				transactionSelect(t, ctx, b, "SELECT v FROM t", false)
				return
			}
			if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(v INT NOT NULL); INSERT INTO t VALUES(99);")); err != nil {
				t.Fatal(err)
			}
			_, err := a.ExecContext(ctx, "SELECT v FROM t")
			transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			transactionAffected(t, ctx, a, "ROLLBACK AND CHAIN", true, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(2)", false, 1)
			transactionSelect(t, ctx, b, "SELECT v FROM t", false, 99)
			transactionAffected(t, ctx, a, "COMMIT", false, 0)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 2, 99)
		})
	}
}

func TestControlTransactionsGatedDump(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d := stuntgres.NewDriver()
	db, peer := openLocal(t, d, t.Name()), openLocal(t, d, t.Name())
	peer.SetMaxOpenConns(1)
	transactionAffected(t, ctx, peer, "CREATE TABLE t(v INT)", false, 0)
	transactionAffected(t, ctx, peer, "INSERT INTO t VALUES(77)", false, 1)
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	defer a.ExecContext(context.Background(), "ROLLBACK")
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	w := &gatedWriter{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- stuntgres.Dump(ctx, peer, w) }()
	released, joined := false, false
	defer func() {
		if !released {
			close(w.release)
		}
		if !joined {
			<-done
		}
	}()
	select {
	case <-w.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	transactionSelect(t, ctx, peer, "SELECT v FROM t", false, 77)
	transactionAffected(t, ctx, peer, "INSERT INTO t VALUES(88)", false, 1)
	transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 77, 88)
	close(w.release)
	released = true
	err = <-done
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	clone := openLocal(t, stuntgres.NewDriver(), t.Name()+"/clone")
	if err = stuntgres.Restore(ctx, clone, bytes.NewReader(w.output.Bytes())); err != nil {
		t.Fatal(err)
	}
	transactionSelect(t, ctx, clone, "SELECT v FROM t", false, 77)
}
