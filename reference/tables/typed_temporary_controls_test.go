package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

const typedTemporaryReplacementError = "stuntgres: catalog replacement with live temporary typed tables of persistent types is unsupported"

func TestReferenceTypedTableTemporaryControls(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS(id INTEGER)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("INSERT INTO t VALUES(11)", 1))
		run(fixture.Exec("CREATE TEMP TABLE tt OF pair(id WITH OPTIONS DEFAULT 7 NOT NULL,CONSTRAINT positive CHECK(id>0))", 0))
		run(fixture.Exec("INSERT INTO tt DEFAULT VALUES", 1))
		want := fixture.Select("SELECT * FROM tt", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		run(want)
		run(fixture.TypedTableAttachment("tt", "pair", true))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM tt", []string{"id"}, []string{"INT4"}, []bool{false}, 1)
		run(fixture.Exec("SET search_path TO pg_temp,public", 0))
		schema := fixture.TempReferenceNamespace(t, &fixture.TempReferenceSession{P: p, C: c}, false)
		nullError := typedTableError("23502", `null value in column "id" of relation "tt" violates not-null constraint`)
		nullError.Detail, nullError.Schema, nullError.Table, nullError.Column = "Failing row contains (null).", schema, "tt", "id"
		checkError := typedTableError("23514", `new row for relation "tt" violates check constraint "positive"`)
		checkError.Detail, checkError.Schema, checkError.Table, checkError.Constraint = "Failing row contains (-1).", schema, "tt", "positive"
		fixture.CompositeReferenceError(t, p, c, "INSERT INTO tt VALUES(NULL)", "execute", nullError)
		fixture.CompositeReferenceError(t, p, c, "INSERT INTO tt VALUES(-1)", "execute", checkError)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), `CREATE TYPE "public"."pair"`) || strings.Contains(dump.String(), `"tt"`) {
			t.Fatal("Dump must retain persistent source and omit TEMP typed table", dump.String())
		}
		held, err := c.QueryContext(p.Ctx, "SELECT * FROM tt")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		stmt, err := c.PrepareContext(p.Ctx, "INSERT INTO tt DEFAULT VALUES")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, replace := range []func() error{func() error { return stuntgres.Reset(p.Ctx, p.Native) }, func() error { return stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())) }} {
			fixture.TransactionOrdinary(t, replace(), typedTemporaryReplacementError)
			run(want)
			run(fixture.TypedTableAttachment("tt", "pair", true))
			run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(after.Bytes(), dump.Bytes()) {
				t.Fatal("rejected replacement changed persistent state", err)
			}
		}
		fixture.TableDDLColumnTypes(t, false, mustInheritanceColumnTypes(t, held), []string{"id"}, []string{"INT4"}, []bool{false})
		fixture.TransactionRowValues(t, held, 7)
		if _, err := stmt.ExecContext(p.Ctx); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT * FROM tt ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
		run(fixture.Exec("DROP TABLE tt", 0))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
	})
}

func TestReferenceTypedTableTemporaryReaderFence(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS(id INTEGER)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("INSERT INTO t VALUES(11)", 1))
		run(fixture.Exec("CREATE TEMP TABLE late(id INTEGER DEFAULT 7 NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO late DEFAULT VALUES", 1))
		attach := func() { run(fixture.Exec("ALTER TABLE late OF pair", 0)) }
		if b.PG {
			attach()
		} else {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			reader := &inheritanceReader{reader: bytes.NewReader(dump.Bytes()), before: attach}
			fixture.TransactionOrdinary(t, stuntgres.Restore(p.Ctx, p.Native, reader), typedTemporaryReplacementError)
			if !reader.started {
				t.Fatal("reader callback not exercised")
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(after.Bytes(), dump.Bytes()) {
				t.Fatal("reader-time rejection changed persistent state", err)
			}
		}
		run(fixture.TypedTableAttachment("late", "pair", true))
		run(fixture.Select("SELECT * FROM late", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM late", []string{"id"}, []string{"INT4"}, []bool{false}, 1)
	})
}
