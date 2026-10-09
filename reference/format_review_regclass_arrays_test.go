package reference_test

import (
	"database/sql"
	"strconv"
	"testing"
)

func TestReferenceFormatReviewRegclassArrays(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(step referenceStep) {
			if !t.Run(step.query, func(t *testing.T) { referenceRunStep(t, p.ctx, c, b.recorder, step) }) {
				t.FailNow()
			}
		}
		run(referenceExec("CREATE TABLE public.format_review_reg_target(v integer)", 0))
		var oid int64
		if err := c.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_catalog.pg_class WHERE relname='format_review_reg_target'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		// Only allocated identity is derived; all presentation strings below are independent.
		array := "{" + strconv.FormatInt(oid, 10) + ",NULL}"
		run(referenceExec("CREATE DOMAIN public.format_review_regs AS regclass[]", 0))
		run(referenceExec("CREATE TABLE public.format_review_reg_store(id integer,a public.format_review_regs,d public.format_review_regs,o text DEFAULT (format('%s','"+array+"'::public.format_review_regs)),e text DEFAULT (format('%s/%L',VARIADIC '"+array+"'::public.format_review_regs)),CONSTRAINT format_review_reg_check CHECK(format('%s',a)=format('%s',d)))", 0))
		insert := func(id int) {
			run(referenceExec("INSERT INTO public.format_review_reg_store(id,a,d) VALUES("+strconv.Itoa(id)+",'"+array+"','"+array+"')", 1))
		}
		insert(1)
		q := "SELECT format('%s',a) AS ordinary,format('%s/%L',VARIADIC a) AS expanded,format('%s',d) AS domain_array,format('%s/%L',VARIADIC d) AS domain_elements FROM public.format_review_reg_store WHERE id=1"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal("quiet recursive REGCLASS array Prepare", err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		live := func(name string) referenceStep {
			canonical := "{" + name + ",NULL}"
			expanded := name + "/NULL"
			return referenceSelect(q, []string{"ordinary", "expanded", "domain_array", "domain_elements"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{canonical, expanded, canonical, expanded}})
		}
		source := func(name string) referenceStep {
			canonical := "{" + name + ",NULL}"
			return referenceSelect("SELECT a::text AS a,d::text AS d FROM public.format_review_reg_store WHERE id=1", []string{"a", "d"}, []string{"TEXT", "TEXT"}, [][]any{{canonical, canonical}})
		}
		assertLive := func(name string) {
			formatReferenceBoth(t, p, b, c, source(name))
			formatReferenceBoth(t, p, b, c, live(name))
			referenceRunStep(t, p.ctx, held, b.recorder, live(name))
			// Re-read the actual typed columns after ordinary and borrowed expansion.
			formatReferenceBoth(t, p, b, c, source(name))
		}
		assertLive("format_review_reg_target")
		var retained *sql.Rows
		if !b.pg {
			retained, err = c.QueryContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		run(referenceExec("SET search_path=pg_catalog", 0))
		assertLive("public.format_review_reg_target")
		insert(2)
		run(referenceExec("ALTER TABLE public.format_review_reg_target RENAME TO format_review_reg_renamed", 0))
		assertLive("public.format_review_reg_renamed")
		insert(3)
		run(referenceExec("SET search_path=public,pg_catalog", 0))
		assertLive("format_review_reg_renamed")
		insert(4)
		if retained != nil {
			before := live("format_review_reg_target")
			stringCoreRetainedRows(t, retained, nil, before.columns, before.types, before.rows, false)
		}
		// Defaults capture the write's path/name; typed source arrays follow current presentation.
		want := referenceSelect("SELECT id,o,e,format('%s',a) AS a,format('%s',d) AS d,format('%s/%L',VARIADIC a) AS expanded FROM public.format_review_reg_store ORDER BY id", []string{"id", "o", "e", "a", "d", "expanded"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
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
		run(referenceExec("SET search_path=public,pg_catalog", 0))
		if err := c.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_catalog.pg_class WHERE relname='format_review_reg_renamed'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		array = "{" + strconv.FormatInt(oid, 10) + ",NULL}"
		insert(5)
		formatReferenceBoth(t, p, b, c, referenceSelect("SELECT o,e,format('%s',a) AS a,format('%s',d) AS d FROM public.format_review_reg_store WHERE id=5", []string{"o", "e", "a", "d"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"{format_review_reg_renamed,NULL}", "format_review_reg_renamed/NULL", "{format_review_reg_renamed,NULL}", "{format_review_reg_renamed,NULL}"}}))
		formatReferenceBoth(t, p, b, c, source("format_review_reg_renamed"))
	})
}
