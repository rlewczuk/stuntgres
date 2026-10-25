package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableAtomicityLateConversion(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id bigint,note varchar(5))", 0))
		run(fixture.Exec("CREATE TABLE a OF pair", 0))
		run(fixture.Exec("CREATE TABLE z OF pair", 0))
		run(fixture.Exec("INSERT INTO a VALUES(11,'hey')", 1))
		run(fixture.Exec("INSERT INTO z VALUES(2147483648,'late')", 1))
		fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair ALTER ATTRIBUTE id TYPE integer CASCADE", "execute", typedTableError("22003", "integer out of range"))
		fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE, ADD ATTRIBUTE id integer CASCADE", "execute", typedTableError("42701", `column "id" of relation "pair" already exists`))
		for _, table := range []string{"a", "z"} {
			value, note := int64(11), "hey"
			if table == "z" {
				value, note = 2147483648, "late"
			}
			run(fixture.Select("SELECT * FROM "+table, []string{"id", "note"}, []string{"INT8", "VARCHAR"}, [][]any{{value, note}}))
			run(fixture.TypedTableAttachment(table, "pair", true))
		}
		run(fixture.Exec("CREATE TABLE probe OF pair", 0))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM probe", []string{"id", "note"}, []string{"INT8", "VARCHAR"}, []bool{true, true}, 0)
	})
}

func TestReferenceTypedTableAtomicityPreCanceled(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note varchar(5))", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("CREATE TABLE ordinary(id integer,note varchar(5))", 0))
		run(fixture.Exec("INSERT INTO t VALUES(11,'hey')", 1))
		// lib/pq does not promise precanceled fast utility rejection; the native
		// public API does. Shared blocked-lock proof is a separate scenario.
		if b.PG {
			return
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, q := range []string{"CREATE TABLE canceled OF pair", "ALTER TABLE ordinary OF pair", "ALTER TABLE t NOT OF", "ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE", "DROP TYPE pair CASCADE"} {
			result, err := c.ExecContext(ctx, q)
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled %q result=%v err=%v", q, result, err)
			}
		}
		run(typedTablePairRows("t", [][]any{{int64(11), "hey"}}))
		run(fixture.TypedTableAttachment("t", "pair", true))
		run(fixture.TypedTableAttachment("ordinary", "pair", false))
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='canceled'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceTypedTableAtomicityLockTimeout(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer,note varchar(5))", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		run(fixture.Exec("INSERT INTO t VALUES(11,'hey')", 1))
		tx, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, typedTablePairRows("t", [][]any{{int64(11), "hey"}}))
		run(fixture.Exec("SET lock_timeout='50ms'", 0))
		fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE", "execute", typedTableError("55P03", "canceling statement due to lock timeout"))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET lock_timeout=0", 0))
		run(typedTablePairRows("t", [][]any{{int64(11), "hey"}}))
		run(fixture.TypedTableAttachment("t", "pair", true))
		run(fixture.Exec("CREATE TABLE probe OF pair", 0))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM probe", []string{"id", "note"}, []string{"INT4", "VARCHAR"}, []bool{true, true}, 0)
	})
}
