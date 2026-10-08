package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/lib/pq"
	"reflect"
	"testing"
	"time"
)

func TestReferenceUpdateCoreBorrowerAndStatementClose(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a := backend.db
			b := p.native
			if backend.pg {
				var err error
				b, err = sql.Open("postgres", referenceDSN)
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close()
			}
			a.SetMaxOpenConns(1)
			a.SetMaxIdleConns(1)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(v INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			borrower, err := a.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				borrower.Close()
				c, err := a.Conn(cleanup)
				if err != nil {
					t.Error(err)
					return
				}
				defer c.Close()
				if _, err := c.ExecContext(cleanup, "ROLLBACK"); err != nil {
					t.Error(err)
				}
			})
			var tuple string
			if backend.pg {
				if err := borrower.QueryRowContext(p.ctx, "SELECT ctid::text FROM t").Scan(&tuple); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = borrower.ExecContext(p.ctx, "BEGIN"); err != nil {
				t.Fatal(err)
			}
			stmt, err := borrower.PrepareContext(p.ctx, "UPDATE t SET v=2")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			result, err := stmt.ExecContext(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			rows, err := borrower.QueryContext(p.ctx, "SELECT v FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if err := borrower.Close(); err != nil {
				t.Fatal(err)
			}
			// Returning the borrower does not dispose the physical explicit owner.
			short, stop := context.WithTimeout(p.ctx, 50*time.Millisecond)
			result, err = b.ExecContext(short, "UPDATE t SET v=3")
			stop()
			if result != nil {
				t.Fatal("blocked update returned result")
			}
			if backend.pg {
				referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: "while updating tuple " + tuple + ` in relation "t"`})
			} else {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("ordinary deadline gained SQLSTATE", err)
				}
			}
			borrower, err = a.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer borrower.Close()
			defer borrower.ExecContext(context.Background(), "ROLLBACK")
			rows, err = borrower.QueryContext(p.ctx, "SELECT v FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(columns, []string{"v"}) {
				t.Fatal(columns, err)
			}
			metadata, err := rows.ColumnTypes()
			scan := reflect.TypeFor[int64]()
			if backend.pg {
				scan = reflect.TypeFor[int32]()
			}
			if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != "INT4" || metadata[0].ScanType() != scan {
				t.Fatal(metadata, err)
			}
			var value int64
			if !rows.Next() {
				t.Fatal("missing reacquired-owner row", rows.Err())
			}
			if err := rows.Scan(&value); err != nil || value != 2 {
				t.Fatal(value, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("reacquired-owner completion", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, borrower, backend.recorder, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
			if _, err := borrower.ExecContext(p.ctx, "ROLLBACK"); err != nil {
				t.Fatal(err)
			}
			result, err = b.ExecContext(p.ctx, "UPDATE t SET v=3")
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			referenceRunStep(t, p.ctx, borrower, backend.recorder, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
		})
	}
}

func TestReferenceUpdateCorePublicPhysicalEnd(t *testing.T) {
	p := newReferenceScenario(t)
	dsn := "reference/" + t.Name()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var connector driver.Connector
			var err error
			if backend.pg {
				connector, err = pq.NewConnector(referenceDSN)
			} else {
				connector, err = p.native.Driver().(driver.DriverContext).OpenConnector(dsn)
			}
			if err != nil {
				t.Fatal(err)
			}
			physical, err := connector.Connect(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer physical.Close()
			peer := sql.OpenDB(connector)
			defer peer.Close()
			execute := physical.(driver.ExecerContext)
			for _, step := range []struct {
				q string
				n int64
			}{{"CREATE TABLE t(v INT)", 0}, {"INSERT INTO t VALUES(1)", 1}, {"BEGIN", 0}, {"UPDATE t SET v=2", 1}} {
				result, err := execute.ExecContext(p.ctx, step.q, nil)
				if err != nil {
					t.Fatal(err)
				}
				referenceLockAffected(t, result, step.n)
			}
			if err := physical.Close(); err != nil {
				t.Fatal(err)
			}
			result, err := peer.ExecContext(p.ctx, "UPDATE t SET v=3")
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
		})
	}
}
