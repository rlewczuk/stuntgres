package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"github.com/ngrok/sqlmw"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableConstraintRetention(t *testing.T) {
	for _, tc := range []struct {
		name, parent, child, constraint string
		checkValue, constraintCount     int
	}{
		{"local_attribute_inherited_constraints", "", "id integer", "positive", -1, 2},
		{"local_attribute_local_constraints", "", "id integer NOT NULL,CONSTRAINT child_positive CHECK(id>1)", "child_positive", 1, 3},
		{"surviving_other_parent", "CREATE TABLE other(id integer DEFAULT 7 NOT NULL,CONSTRAINT positive CHECK(id>0))", "", "positive", -1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
				run(fixture.Exec("CREATE TABLE t OF pair(id DEFAULT 7 NOT NULL,CONSTRAINT positive CHECK(id>0))", 0))
				parents := "t"
				if tc.parent != "" {
					run(fixture.Exec(tc.parent, 0))
					parents += ",other"
				}
				run(fixture.Exec("CREATE TABLE child("+tc.child+") INHERITS("+parents+")", 0))
				run(fixture.Exec("ALTER TYPE pair DROP ATTRIBUTE id CASCADE", 0))
				fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM child LIMIT 0", []string{"id"}, []string{"INT4"}, []bool{false}, 0)
				count := fixture.Select("SELECT count(*) AS n FROM pg_constraint WHERE conrelid='child'::regclass::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(tc.constraintCount)}})
				run(count)
				// Leaf-child INSERT/scan already belongs to the existing inheritance
				// surface; recursive parent scans/DML/ALTER are not enabled here.
				run(fixture.Exec("INSERT INTO child VALUES(11)", 1))
				run(fixture.Exec("INSERT INTO child DEFAULT VALUES", 1))
				nullError := typedTableError("23502", `null value in column "id" of relation "child" violates not-null constraint`)
				nullError.Detail, nullError.Schema, nullError.Table, nullError.Column = "Failing row contains (null).", "public", "child", "id"
				fixture.CompositeReferenceError(t, p, c, "INSERT INTO child VALUES(NULL)", "execute", nullError)
				checkError := typedTableError("23514", `new row for relation "child" violates check constraint "`+tc.constraint+`"`)
				value := "-1"
				if tc.checkValue == 1 {
					value = "1"
				}
				checkError.Detail, checkError.Schema, checkError.Table, checkError.Constraint = "Failing row contains ("+value+").", "public", "child", tc.constraint
				fixture.CompositeReferenceError(t, p, c, "INSERT INTO child VALUES("+value+")", "execute", checkError)
				state := fixture.Select("SELECT * FROM child ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(11)}})
				run(state)
				run(fixture.TypedTableAttachment("t", "pair", true))
				if b.PG {
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				freshDriver := stuntgres.NewDriver()
				fresh := fixture.DriverPool(t, freshDriver, "typed-retained-fresh")
				connector, err := freshDriver.OpenConnector("typed-retained-fresh")
				if err != nil {
					t.Fatal(err)
				}
				wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: connector}, fixture.Interceptor{})
				recordedConnector, err := wrapped.(driver.DriverContext).OpenConnector("")
				if err != nil {
					t.Fatal(err)
				}
				recorded := sql.OpenDB(recordedConnector)
				defer recorded.Close()
				for _, pools := range []struct{ control, observe *sql.DB }{{fresh, recorded}, {p.Native, b.DB}} {
					if err := stuntgres.Reset(p.Ctx, pools.control); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, pools.control, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					for _, step := range []fixture.Step{state, count, fixture.TypedTableAttachment("t", "pair", true)} {
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, pools.observe, p.MemoryRecord, step)
					}
					fixture.TableDDLMetadata(t, p.Ctx, pools.observe, false, prepared, "SELECT * FROM child", []string{"id"}, []string{"INT4"}, []bool{false}, 2)
					connection, err := pools.observe.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					fixture.CompositeReferenceError(t, p, connection, "INSERT INTO child VALUES(NULL)", "execute", nullError)
					fixture.CompositeReferenceError(t, p, connection, "INSERT INTO child VALUES("+value+")", "execute", checkError)
					if err := connection.Close(); err != nil {
						t.Fatal(err)
					}
					var repeated bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, pools.control, &repeated); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, pools.control, bytes.NewReader(repeated.Bytes())); err != nil {
						t.Fatal(err)
					}
					for _, step := range []fixture.Step{state, count, fixture.TypedTableAttachment("t", "pair", true)} {
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, pools.observe, p.MemoryRecord, step)
					}
					fixture.TableDDLMetadata(t, p.Ctx, pools.observe, false, prepared, "SELECT * FROM child", []string{"id"}, []string{"INT4"}, []bool{false}, 2)
					repeatedConnection, err := pools.observe.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					fixture.CompositeReferenceError(t, p, repeatedConnection, "INSERT INTO child VALUES(NULL)", "execute", nullError)
					fixture.CompositeReferenceError(t, p, repeatedConnection, "INSERT INTO child VALUES("+value+")", "execute", checkError)
					if err := repeatedConnection.Close(); err != nil {
						t.Fatal(err)
					}
					insertDefault := fixture.Exec("INSERT INTO child DEFAULT VALUES", 1)
					insertDefault.Prepared = prepared
					fixture.RunStep(t, p.Ctx, pools.observe, p.MemoryRecord, insertDefault)
					step := fixture.Select("SELECT * FROM child ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(11)}})
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, pools.observe, p.MemoryRecord, step)
				}
				if p.Postgres != nil {
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatal("actual retained-child typed dump replay", err)
					}
					for _, step := range []fixture.Step{state, count, fixture.Exec("INSERT INTO child DEFAULT VALUES", 1), fixture.Select("SELECT * FROM child ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(11)}})} {
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
					}
					connection, err := p.Postgres.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					fixture.CompositeReferenceError(t, p, connection, "INSERT INTO child VALUES(NULL)", "execute", nullError)
					fixture.CompositeReferenceError(t, p, connection, "INSERT INTO child VALUES("+value+")", "execute", checkError)
					if err := connection.Close(); err != nil {
						t.Fatal(err)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
}
