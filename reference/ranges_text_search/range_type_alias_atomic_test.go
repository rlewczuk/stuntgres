package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Actual source identities, not allocator goldens, distinguish an unsatisfiable
// dangling numeric bound from a large, portable numeric upper bound.
func TestReferenceRangeTypeAliasNumericAnchor(t *testing.T) {
	for _, positive := range []bool{false, true} {
		name := "unsatisfiable"
		if positive {
			name = "representable_numeric"
		}
		t.Run(name, func(t *testing.T) { rangeTypeAliasNumericAnchor(t, positive) })
	}
}

func rangeTypeAliasNumericAnchor(t *testing.T, positive bool) {
	fixture.SchemaReferenceCleanup(t, "range_alias_atomic")
	fixture.SchemaReferenceCleanup(t, "range_alias_padding")
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{"CREATE SCHEMA range_alias_atomic", "CREATE SCHEMA range_alias_padding", "CREATE TABLE range_alias_atomic.lo(k integer)", "CREATE TYPE alias_atomic_span AS RANGE(SUBTYPE=regclass,SUBTYPE_OPCLASS=pg_catalog.oid_ops,MULTIRANGE_TYPE_NAME=alias_atomic_many)", "CREATE TABLE alias_atomic_saved(k integer,r alias_atomic_span,m alias_atomic_many)"} {
			run(fixture.Exec(q, 0))
		}
		var lo int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'range_alias_atomic.lo'::regclass::oid::bigint").Scan(&lo); err != nil {
			t.Fatal(err)
		}
		numeric := lo + 1
		if positive {
			numeric = 4294967295
		}
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE oid=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, numeric))
		span := func(upper int64) string {
			return "public.alias_atomic_span('range_alias_atomic.lo'::regclass," + fmt.Sprint(upper) + "::oid::regclass,'[]')"
		}
		run(fixture.Exec("INSERT INTO alias_atomic_saved VALUES(1,"+span(numeric)+",public.alias_atomic_many("+span(numeric)+"))", 1))
		sourceCheck := fixture.Select("SELECT k,lower(r)::oid::bigint AS lo,upper(r)::oid::bigint AS hi,isempty(r) AS empty,m::text AS m FROM alias_atomic_saved", []string{"k", "lo", "hi", "empty", "m"}, []string{"INT4", "INT8", "INT8", "BOOL", "TEXT"}, [][]any{{int64(1), lo, numeric, false, "{[range_alias_atomic.lo," + fmt.Sprint(numeric) + "]}"}})
		rangeTypeRemedyRoutes(t, p, b, c, sourceCheck)
		prepared, err := c.PrepareContext(p.Ctx, sourceCheck.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.Close()
		held, err := prepared.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if b.PG {
			fixture.RangeConsumeHeld(t, held, sourceCheck)
			return
		}
		var script bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
			t.Fatal(err)
		}
		target := fixture.NewFixture(t, false, true)
		owner, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		targetRun := func(s fixture.Step) { fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, s) }
		targetRun(fixture.Exec("CREATE TEMP TABLE alias_atomic_occupied(k integer)", 0))
		targetRun(fixture.Exec("INSERT INTO alias_atomic_occupied VALUES(7)", 1))
		collision := fixture.Select("SELECT 'pg_temp.alias_atomic_occupied'::regclass::oid::bigint=$1::bigint AS collision", []string{"collision"}, []string{"BOOL"}, [][]any{{true}}, lo)
		targetRun(collision)
		targetRun(fixture.Exec("CREATE TABLE untouched(k integer)", 0))
		targetRun(fixture.Exec("INSERT INTO untouched VALUES(9)", 1))
		old := fixture.Select("SELECT k FROM untouched", []string{"k"}, []string{"INT4"}, [][]any{{int64(9)}})
		oldRows, err := owner.QueryContext(p.Ctx, old.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer oldRows.Close()
		oldStatement, err := owner.PrepareContext(p.Ctx, old.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer oldStatement.Close()
		var before, after bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &before); err != nil {
			t.Fatal(err)
		}
		err = stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(script.Bytes()))
		if !positive {
			var diagnostic interface{ SQLState() string }
			if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "22000" || !strings.Contains(err.Error(), fmt.Sprint(numeric)) {
				t.Fatal("unrepresentable destination bounds must identify the numeric constraint before publication", err)
			}
			if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("rejected Restore published persistent state")
			}
			targetRun(old)
			heldOld, err := oldStatement.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RangeConsumeHeld(t, heldOld, old)
		} else if err != nil {
			t.Fatal("representable numeric upper was rejected", err)
		}
		fixture.RangeConsumeHeld(t, oldRows, old)
		targetRun(fixture.Select("SELECT k FROM alias_atomic_occupied", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
		targetRun(collision)
		fixture.RangeConsumeHeld(t, held, sourceCheck)
		heldAgain, err := prepared.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.RangeConsumeHeld(t, heldAgain, sourceCheck)
		run(sourceCheck)
		var sourceAfter bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &sourceAfter); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(script.Bytes(), sourceAfter.Bytes()) {
			t.Fatal("target replacement changed source state")
		}
		if !positive {
			// Reuse the rejected target: a valid corrected family must not remain poisoned.
			numeric = 4294967295
			run(fixture.Exec("UPDATE alias_atomic_saved SET r="+span(numeric)+",m=public.alias_atomic_many("+span(numeric)+")", 1))
			script.Reset()
			if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(script.Bytes())); err != nil {
				t.Fatal("corrected numeric replacement after atomic rejection", err)
			}
		}
		destCheck := func() {
			targetRun(fixture.Select("SELECT lower(r)='range_alias_atomic.lo'::regclass AS lo,upper(r)::oid::bigint=$1::bigint AS hi,isempty(r) AS empty,m::text AS m FROM alias_atomic_saved WHERE k=1", []string{"lo", "hi", "empty", "m"}, []string{"BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{true, true, false, "{[range_alias_atomic.lo," + fmt.Sprint(numeric) + "]}"}}, numeric))
		}
		destCheck()
		targetRun(fixture.Exec("INSERT INTO alias_atomic_saved VALUES(2,"+span(numeric)+",public.alias_atomic_many("+span(numeric)+"))", 1))
		var redump bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(redump.Bytes())); err != nil {
			t.Fatal(err)
		}
		destCheck()
		targetRun(collision)
		if p.Postgres != nil {
			// The large-upper positive stream is portable; the unsatisfiable stream's
			// actual PG source validity above is intentional, not a claim of replay success.
			clear := "DROP SCHEMA IF EXISTS range_alias_atomic CASCADE;DROP SCHEMA IF EXISTS range_alias_padding CASCADE;DROP SCHEMA public CASCADE;CREATE SCHEMA public;"
			if _, err := p.Postgres.ExecContext(p.Ctx, clear); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, redump.String()); err != nil {
				t.Fatal("actual representable numeric dump PG replay", err)
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT count(*) AS n FROM alias_atomic_saved WHERE lower(r)='range_alias_atomic.lo'::regclass AND upper(r)::oid::bigint=$1::bigint AND NOT isempty(r)", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}, numeric))
			if _, err := p.Postgres.ExecContext(p.Ctx, clear); err != nil {
				t.Fatal(err)
			}
		}
	})
}
