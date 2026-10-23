package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeAliasOrdering(t *testing.T) {
	for _, tc := range []struct{ subtype, fixed string }{{"regclass", "'pg_class'::regclass"}, {"regtype", "'integer'::regtype"}} {
		t.Run(tc.subtype, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "range_order")
			fixture.SchemaReferenceCleanup(t, "range_order_padding")
			rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				// TEMP creates both pg_temp and pg_toast_temp namespaces. Match those two
				// allocations before the two tables so both real collisions are exercised.
				// The collision is asserted using actual identities, never numeric goldens.
				for _, q := range []string{"CREATE SCHEMA range_order", "CREATE SCHEMA range_order_padding", "CREATE TABLE range_order.lo(k integer)", "CREATE TABLE range_order.hi(k integer)",
					"CREATE TYPE alias_span AS RANGE(SUBTYPE=" + tc.subtype + ",SUBTYPE_OPCLASS=pg_catalog.oid_ops,MULTIRANGE_TYPE_NAME=alias_many)",
					"CREATE DOMAIN alias_domain AS alias_span",
					"CREATE TABLE alias_saved(k integer,r alias_span,m alias_many,a alias_span[],d alias_domain)",
				} {
					run(fixture.Exec(q, 0))
				}
				lo, hi := "'range_order.lo'::"+tc.subtype, "'range_order.hi'::"+tc.subtype
				var loID, hiID int64
				if err := c.QueryRowContext(p.Ctx, "SELECT ("+lo+")::oid::bigint,("+hi+")::oid::bigint").Scan(&loID, &hiID); err != nil || loID >= hiID {
					t.Fatal("source finite endpoint order", loID, hiID, err)
				}
				makeRange := func(lower, upper string) string { return "public.alias_span(" + lower + "," + upper + ",'[]')" }
				finite := makeRange(lo, hi)
				left := makeRange(lo, lo)
				right := makeRange(hi, hi)
				anchor := makeRange(tc.fixed, hi)
				fixed := makeRange(tc.fixed, tc.fixed)
				run(fixture.Exec("INSERT INTO alias_saved VALUES(1,"+finite+",alias_many("+right+","+left+"),ARRAY["+finite+"],"+finite+"),(2,"+anchor+",alias_many("+right+","+fixed+"),ARRAY["+anchor+"],"+anchor+")", 2))
				fixedName := "pg_class"
				if tc.subtype == "regtype" {
					fixedName = "integer"
				}
				q := "SELECT k,r::text AS r,m::text AS m,lower(r)::oid=(CASE WHEN k=2 THEN (" + tc.fixed + ")::oid ELSE (" + lo + ")::oid END) AS lower_identity,upper(r)::oid=(" + hi + ")::oid AS upper_identity,(r @> " + hi + ") AS contains_hi,isempty(r) AS empty,a[1]::text AS nested,d::text AS domain_value FROM alias_saved ORDER BY k"
				want := [][]any{
					{int64(1), "[range_order.lo,range_order.hi]", "{[range_order.lo,range_order.lo],[range_order.hi,range_order.hi]}", true, true, true, false, "[range_order.lo,range_order.hi]", "[range_order.lo,range_order.hi]"},
					{int64(2), "[" + fixedName + ",range_order.hi]", "{[" + fixedName + "," + fixedName + "],[range_order.hi,range_order.hi]}", true, true, true, false, "[" + fixedName + ",range_order.hi]", "[" + fixedName + ",range_order.hi]"},
				}
				step := func(rows [][]any) fixture.Step {
					return fixture.Select(q, []string{"k", "r", "m", "lower_identity", "upper_identity", "contains_hi", "empty", "nested", "domain_value"}, []string{"INT4", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT"}, rows)
				}
				check := func(backend fixture.Backend, executor fixture.Executor, rows [][]any) {
					rangeTypeRemedyRoutes(t, p, backend, executor, step(rows))
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT ("+lo+")::oid < ("+hi+")::oid AS ordered,("+tc.fixed+")::oid < ("+lo+")::oid AS anchor_before", []string{"ordered", "anchor_before"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT count(*) AS n FROM alias_saved CROSS JOIN LATERAL unnest(m) AS members(v)", []string{"n"}, []string{"INT8"}, [][]any{{int64(len(rows) * 2)}}))
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT k FROM alias_saved WHERE NOT (r @> lower(r)) ORDER BY k", []string{"k"}, []string{"INT4"}, nil))
				}
				check(b, c, want)
				prepared, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer prepared.Close()
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: prepared, SQL: q}, b.Recorder, step(want))
				if b.PG {
					return
				}
				held, err := c.QueryContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				var script bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
					t.Fatal(err)
				}
				for _, occupied := range []int{1, 2} {
					t.Run(fmt.Sprintf("occupied_%d", occupied), func(t *testing.T) {
						target := fixture.NewFixture(t, false, true)
						owner, err := target.Memory.Conn(target.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer owner.Close()
						for index := 1; index <= occupied; index++ {
							name := fmt.Sprintf("order_collision_%d", index)
							fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, fixture.Exec("CREATE TEMP TABLE "+name+"(k integer)", 0))
							expected := loID
							if index == 2 {
								expected = hiID
							}
							fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, fixture.Select("SELECT ('pg_temp."+name+"'::"+tc.subtype+")::oid::bigint=$1::bigint AS collision", []string{"collision"}, []string{"BOOL"}, [][]any{{true}}, expected))
						}
						if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(script.Bytes())); err != nil {
							t.Fatal("ordered alias Restore", err)
						}
						dest := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
						check(dest, owner, want)
						fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, fixture.Prepared(fixture.Exec("INSERT INTO alias_saved VALUES(3,"+finite+",alias_many("+left+","+right+"),ARRAY["+finite+"],"+finite+")", 1)))
						after := append(append([][]any(nil), want...), append([]any(nil), want[0]...))
						after[2][0] = int64(3)
						check(dest, owner, after)
						var redump bytes.Buffer
						if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(redump.Bytes())); err != nil {
							t.Fatal("ordered alias redump Restore", err)
						}
						check(dest, owner, after)
						fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, fixture.Select("SELECT k FROM order_collision_1", []string{"k"}, []string{"INT4"}, nil))
						if p.Postgres != nil {
							clear := "DROP SCHEMA IF EXISTS range_order CASCADE;DROP SCHEMA IF EXISTS range_order_padding CASCADE;DROP SCHEMA public CASCADE;CREATE SCHEMA public;"
							if _, err := p.Postgres.ExecContext(p.Ctx, clear); err != nil {
								t.Fatal(err)
							}
							if _, err := p.Postgres.ExecContext(p.Ctx, redump.String()); err != nil {
								t.Fatal("ordered destination dump PG replay", err)
							}
							check(fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}, p.Postgres, after)
							if _, err := p.Postgres.ExecContext(p.Ctx, clear); err != nil {
								t.Fatal(err)
							}
						}
					})
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: prepared, SQL: q}, b.Recorder, step(want))
				rangeConsumeHeld(t, held, step(want))
				check(b, c, want)
			})
		})
	}
}
