package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type roleTransactionFailedReader struct{ failure error }

func (r roleTransactionFailedReader) Read([]byte) (int, error) { return 0, r.failure }

func TestReferenceRoleTransactionalNativeControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		for _, held := range []bool{false, true} {
			label := "direct"
			if held {
				label = "held"
			}
			t.Run(replacement+"/"+label, func(t *testing.T) {
				roleTransactionT2Cases(t, []string{"rc_public", "rc_private", "rc_member"}, []string{"rc_private_schema"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					c, _ := fixture.RoleTransactionConn(t, p, b)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range []string{"CREATE ROLE rc_public", "CREATE ROLE rc_member", "CREATE TABLE rc_committed(i INT)"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO rc_committed VALUES(7)", 1))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("SAVEPOINT before_private", 0))
					for _, q := range []string{"CREATE ROLE rc_private LOGIN IN ROLE rc_public", "ALTER ROLE rc_public CREATEDB", "GRANT rc_public TO rc_member", "CREATE SCHEMA rc_private_schema", "CREATE TABLE rc_private_schema.items(i INT)"} {
						fixture.RoleTransactionStep(t, p, b, c, held, fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO rc_private_schema.items VALUES(8)", 1))
					run(fixture.Exec("INSERT INTO rc_committed VALUES(9)", 1))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.RoleTransactionExists("rc_private", false))
					if b.PG {
						run(fixture.Exec("ROLLBACK", 0))
						run(fixture.RoleTransactionExists("rc_private", false))
						run(fixture.Select("SELECT i FROM rc_committed", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						return
					}
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if dump.Len() == 0 || strings.Contains(dump.String(), "rc_private") || strings.Contains(dump.String(), "CREATE ROLE") {
						t.Fatal("dump exposed private/role state or was empty")
					}
					connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/replay")
					if err != nil {
						t.Fatal(err)
					}
					clone := sql.OpenDB(connector)
					defer clone.Close()
					if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					rows, err := clone.QueryContext(p.Ctx, "SELECT i FROM rc_committed")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.Columns()
					if err != nil || len(columns) != 1 || columns[0] != "i" {
						t.Fatal("replayed sentinel columns", columns, err)
					}
					metadata, err := rows.ColumnTypes()
					if err != nil || len(metadata) != 1 || metadata[0].Name() != "i" || metadata[0].DatabaseTypeName() != "INT4" || metadata[0].ScanType() != reflect.TypeFor[int64]() {
						t.Fatal("replayed sentinel metadata", metadata, err)
					}
					var value int64
					if !rows.Next() {
						t.Fatal("replayed sentinel missing", rows.Err())
					}
					if err := rows.Scan(&value); err != nil || value != 7 {
						t.Fatal("replayed sentinel value", value, err)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("replayed sentinel cardinality", rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					failure := errors.New("role transaction restore reader failure")
					if err := stuntgres.Restore(p.Ctx, p.Native, roleTransactionFailedReader{failure}); !errors.Is(err, failure) {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE rc_partial(i INT); INSERT INTO rc_partial VALUES('bad');")); err == nil {
						t.Fatal("invalid restore succeeded")
					}
					run(fixture.RoleTransactionExists("rc_private", true))
					run(fixture.Select("SELECT i FROM rc_private_schema.items", []string{"i"}, []string{"INT4"}, [][]any{{int64(8)}}))
					stmt, err := c.PrepareContext(p.Ctx, "ALTER ROLE rc_private NOLOGIN")
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					retained, err := c.QueryContext(p.Ctx, "SELECT rolname FROM pg_roles WHERE rolname='rc_private'")
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					if replacement == "reset" {
						err = stuntgres.Reset(p.Ctx, p.Native)
					} else {
						err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
					}
					if err != nil {
						t.Fatal(err)
					}
					types, err := retained.ColumnTypes()
					if err != nil || len(types) != 1 || types[0].Name() != "rolname" || types[0].DatabaseTypeName() != "NAME" {
						t.Fatal("retained role metadata", types, err)
					}
					var name string
					if !retained.Next() || retained.Scan(&name) != nil || name != "rc_private" || retained.Next() || retained.Err() != nil {
						t.Fatal("retained role identity", name, retained.Err())
					}
					retained.Close()
					_, err = stmt.ExecContext(p.Ctx)
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					_, err = c.ExecContext(p.Ctx, "ROLLBACK TO before_private")
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					_, err = c.ExecContext(p.Ctx, "COMMIT")
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.RoleTransactionExists("rc_private", false))
					run(fixture.RoleTransactionExists("rc_public", true))
					run(fixture.Select("SELECT rolcreatedb FROM pg_roles WHERE rolname='rc_public'", []string{"rolcreatedb"}, []string{"BOOL"}, [][]any{{false}}))
					run(fixture.Select("SELECT pg_has_role('rc_member','rc_public','MEMBER') AS member", []string{"member"}, []string{"BOOL"}, [][]any{{false}}))
					run(fixture.Exec("CREATE ROLE rc_private", 0))
					run(fixture.Exec("DROP ROLE rc_private", 0))
				})
			})
		}
	}
	t.Run("physical_close", func(t *testing.T) {
		roleTransactionT2Cases(t, []string{"rc_abandoned"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c, _ := fixture.RoleTransactionConn(t, p, b)
			for _, q := range []string{"BEGIN", "CREATE ROLE rc_abandoned"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.RoleTransactionExists("rc_abandoned", false))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE ROLE rc_abandoned", 0))
		})
	})
}
