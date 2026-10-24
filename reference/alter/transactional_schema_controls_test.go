package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalSchemaNativeControls(t *testing.T) {
	transactionalSchemaAbandonment(t)
	transactionalSchemaFailedRestoreCommit(t)
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "tx_control_private")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE tx_control_committed(v INT PRIMARY KEY)", 0))
					run(fixture.Exec("INSERT INTO tx_control_committed VALUES(77)", 1))
					run(fixture.Exec("CREATE SEQUENCE tx_control_counter", 0))
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("CREATE SCHEMA tx_control_private CREATE TABLE owned(id SERIAL PRIMARY KEY, v INT DEFAULT 4)", 0))
					run(fixture.Exec("INSERT INTO tx_control_private.owned(v) VALUES(DEFAULT)", 1))
					run(fixture.Exec("INSERT INTO tx_control_committed VALUES(1)", 1))
					run(fixture.Exec("SAVEPOINT before_counter", 0))
					run(fixture.Select("SELECT nextval('tx_control_counter')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
					run(fixture.Exec("ROLLBACK TO before_counter", 0))
					run(fixture.Select("SELECT currval('tx_control_counter')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT v FROM tx_control_committed", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if strings.Contains(dump.String(), "tx_control_private") {
							t.Fatal("dump exposed private namespace", dump.String())
						}
						connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/clone")
						if err != nil {
							t.Fatal(err)
						}
						clone := sql.OpenDB(connector)
						defer clone.Close()
						if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						cloneRows, err := clone.QueryContext(p.Ctx, "SELECT v FROM tx_control_committed")
						fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: cloneRows, Err: err}, false, 77)
						d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "tx_control_committed_pkey"`)
						d.Detail = "Key (v)=(77) already exists."
						d.Schema = "public"
						d.Table = "tx_control_committed"
						d.Constraint = "tx_control_committed_pkey"
						_, err = clone.ExecContext(p.Ctx, "INSERT INTO tx_control_committed VALUES(77)")
						fixture.AssertDiagnostic(t, err, &d)
						readErr := errors.New("transactional schema reader failure")
						if err := stuntgres.Restore(p.Ctx, p.Native, addConstraintFailedReader{readErr}); !errors.Is(err, readErr) {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE tx_restore_partial(v INT); INSERT INTO tx_restore_partial VALUES('bad');")); err == nil {
							t.Fatal("invalid restore succeeded")
						}
						run(fixture.Select("SELECT id,v FROM tx_control_private.owned", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(4)}}))
						held, err := c.PrepareContext(p.Ctx, "INSERT INTO tx_control_private.owned(v) VALUES(6)")
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						rows, err := c.QueryContext(p.Ctx, "SELECT id,v FROM tx_control_private.owned")
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						columns, err := rows.Columns()
						if err != nil {
							t.Fatal(err)
						}
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 2 {
							t.Fatal(types, err)
						}
						for _, typ := range types {
							if typ.DatabaseTypeName() != "INT4" {
								t.Fatal(typ.DatabaseTypeName())
							}
						}
						if replacement == "reset" {
							err = stuntgres.Reset(p.Ctx, p.Native)
						} else {
							err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
						}
						if err != nil {
							t.Fatal(err)
						}
						if len(columns) != 2 || columns[0] != "id" || columns[1] != "v" {
							t.Fatal(columns)
						}
						after, err := rows.ColumnTypes()
						if err != nil || len(after) != 2 || after[0].Name() != "id" || after[1].Name() != "v" || after[0].DatabaseTypeName() != "INT4" || after[1].DatabaseTypeName() != "INT4" {
							t.Fatal(after, err)
						}
						if !rows.Next() {
							t.Fatal(rows.Err())
						}
						var id, v int64
						if err := rows.Scan(&id, &v); err != nil || id != 1 || v != 4 {
							t.Fatal(id, v, err)
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal(rows.Err())
						}
						rows.Close()
						_, err = held.ExecContext(p.Ctx)
						fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
						_, err = c.ExecContext(p.Ctx, "ROLLBACK TO before_counter")
						fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
						_, err = c.ExecContext(p.Ctx, "COMMIT")
						fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
						run(fixture.Exec("ROLLBACK", 0))
						run(fixture.Exec("CREATE TABLE tx_control_reused(v INT)", 0))
						run(fixture.Exec("INSERT INTO tx_control_reused VALUES(8)", 1))
						run(fixture.Select("SELECT v FROM tx_control_reused", []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}))
					} else {
						run(fixture.Exec("ROLLBACK", 0))
						run(fixture.Select("SELECT currval('tx_control_counter')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname='tx_control_private'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				})
			}
		})
	}
}
