package reference_test

import (
	"database/sql"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFormatReviewRegclassArrays(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(step fixture.Step) {
			if !t.Run(step.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }) {
				t.FailNow()
			}
		}
		run(fixture.Exec("CREATE TABLE public.format_review_reg_target(v integer)", 0))
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_catalog.pg_class WHERE relname='format_review_reg_target'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		// Only allocated identity is derived; all presentation strings below are independent.
		array := "{" + strconv.FormatInt(oid, 10) + ",NULL}"
		run(fixture.Exec("CREATE DOMAIN public.format_review_regs AS regclass[]", 0))
		run(fixture.Exec("CREATE TABLE public.format_review_reg_store(id integer,a public.format_review_regs,d public.format_review_regs,o text DEFAULT (format('%s','"+array+"'::public.format_review_regs)),e text DEFAULT (format('%s/%L',VARIADIC '"+array+"'::public.format_review_regs)),CONSTRAINT format_review_reg_check CHECK(format('%s',a)=format('%s',d)))", 0))
		insert := func(id int) {
			run(fixture.Exec("INSERT INTO public.format_review_reg_store(id,a,d) VALUES("+strconv.Itoa(id)+",'"+array+"','"+array+"')", 1))
		}
		insert(1)
		q := "SELECT format('%s',a) AS ordinary,format('%s/%L',VARIADIC a) AS expanded,format('%s',d) AS domain_array,format('%s/%L',VARIADIC d) AS domain_elements FROM public.format_review_reg_store WHERE id=1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("quiet recursive REGCLASS array Prepare", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		live := func(name string) fixture.Step {
			canonical := "{" + name + ",NULL}"
			expanded := name + "/NULL"
			return fixture.Select(q, []string{"ordinary", "expanded", "domain_array", "domain_elements"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{canonical, expanded, canonical, expanded}})
		}
		source := func(name string) fixture.Step {
			canonical := "{" + name + ",NULL}"
			return fixture.Select("SELECT a::text AS a,d::text AS d FROM public.format_review_reg_store WHERE id=1", []string{"a", "d"}, []string{"TEXT", "TEXT"}, [][]any{{canonical, canonical}})
		}
		assertLive := func(name string) {
			formatReferenceBoth(t, p, b, c, source(name))
			formatReferenceBoth(t, p, b, c, live(name))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, live(name))
			// Re-read the actual typed columns after ordinary and borrowed expansion.
			formatReferenceBoth(t, p, b, c, source(name))
		}
		assertLive("format_review_reg_target")
		var retained *sql.Rows
		if !b.PG {
			retained, err = c.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		run(fixture.Exec("SET search_path=pg_catalog", 0))
		assertLive("public.format_review_reg_target")
		insert(2)
		run(fixture.Exec("ALTER TABLE public.format_review_reg_target RENAME TO format_review_reg_renamed", 0))
		assertLive("public.format_review_reg_renamed")
		insert(3)
		run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		assertLive("format_review_reg_renamed")
		insert(4)
		if retained != nil {
			before := live("format_review_reg_target")
			fixture.StringCoreRetainedRows(t, retained, nil, before.Columns, before.Types, before.Rows, false)
		}
		// Defaults capture the write's path/name; typed source arrays follow current presentation.
		want := fixture.Select("SELECT id,o,e,format('%s',a) AS a,format('%s',d) AS d,format('%s/%L',VARIADIC a) AS expanded FROM public.format_review_reg_store ORDER BY id", []string{"id", "o", "e", "a", "d", "expanded"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
			{int64(1), "{format_review_reg_target,NULL}", "format_review_reg_target/NULL", "{format_review_reg_renamed,NULL}", "{format_review_reg_renamed,NULL}", "format_review_reg_renamed/NULL"},
			{int64(2), "{public.format_review_reg_target,NULL}", "public.format_review_reg_target/NULL", "{format_review_reg_renamed,NULL}", "{format_review_reg_renamed,NULL}", "format_review_reg_renamed/NULL"},
			{int64(3), "{public.format_review_reg_renamed,NULL}", "public.format_review_reg_renamed/NULL", "{format_review_reg_renamed,NULL}", "{format_review_reg_renamed,NULL}", "format_review_reg_renamed/NULL"},
			{int64(4), "{format_review_reg_renamed,NULL}", "format_review_reg_renamed/NULL", "{format_review_reg_renamed,NULL}", "{format_review_reg_renamed,NULL}", "format_review_reg_renamed/NULL"},
		})
		formatReferenceBoth(t, p, b, c, want)
		formatReferenceControlPhase(t, p, b, c, want)
		formatReferenceBoth(t, p, b, c, source("format_review_reg_renamed"))
		// After native replay, exercise reminted DEFAULT/CHECK expressions again;
		// PostgreSQL executes the equivalent second write without fake controls.
		run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_catalog.pg_class WHERE relname='format_review_reg_renamed'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		array = "{" + strconv.FormatInt(oid, 10) + ",NULL}"
		insert(5)
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT o,e,format('%s',a) AS a,format('%s',d) AS d FROM public.format_review_reg_store WHERE id=5", []string{"o", "e", "a", "d"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"{format_review_reg_renamed,NULL}", "format_review_reg_renamed/NULL", "{format_review_reg_renamed,NULL}", "{format_review_reg_renamed,NULL}"}}))
		formatReferenceBoth(t, p, b, c, source("format_review_reg_renamed"))
	})
}
