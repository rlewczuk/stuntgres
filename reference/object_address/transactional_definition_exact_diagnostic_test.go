package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 GetDefaultOpClass flattens domains before counting exact defaults,
// then formats that base identity using format_type_be and current visibility.
// Incomplete classes suffice: ambiguity precedes support/provider validation.
func TestReferenceTransactionalDefinitionExactDiagnostics(t *testing.T) {
	definitionScenario(t, []string{"tx_exact_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defer c.ExecContext(p.Ctx, `ROLLBACK`)
		runA := definitionRun(t, p, b, a)
		runC := definitionRun(t, p, b, c)
		runA(fixture.Exec(`CREATE TYPE public.tx_exact_label AS ENUM('low','high')`, 0))
		runA(fixture.Exec(`CREATE DOMAIN public.tx_exact_domain AS public.tx_exact_label`, 0))
		runA(fixture.Exec(`CREATE ACCESS METHOD tx_exact_am TYPE INDEX HANDLER bthandler`, 0))
		runA(fixture.Exec(`CREATE TABLE public.tx_exact_rows(v public.tx_exact_label, d public.tx_exact_domain)`, 0))
		method := definitionID(t, p, a, `SELECT oid::bigint FROM pg_am WHERE amname='tx_exact_am'`)
		input := definitionID(t, p, a, `SELECT oid::bigint FROM pg_type WHERE typname='tx_exact_label'`)
		table := definitionID(t, p, a, `SELECT oid::bigint FROM pg_class WHERE relname='tx_exact_rows'`)
		queries := []string{
			`CREATE INDEX tx_exact_enum_index ON public.tx_exact_rows USING tx_exact_am(v)`,
			`CREATE INDEX tx_exact_domain_index ON public.tx_exact_rows USING tx_exact_am(d)`,
		}
		// Retain these very handles across both commits and search_path changes.
		// Quiet Prepare must succeed before either DEFAULT exists, without effects.
		held := []fixture.HeldStatement{
			definitionHeld(t, p, a, queries[0]),
			definitionHeld(t, p, a, queries[1]),
		}
		noEffects := func() {
			t.Helper()
			runA(definitionCount(`SELECT count(*) FROM pg_class WHERE relname IN ('tx_exact_enum_index','tx_exact_domain_index')`, 0))
			runA(definitionCount(`SELECT count(*) FROM pg_index WHERE indrelid=$1::oid`, 0, table))
			// Enumerate all relation-to-class edges for the method, including
			// orphan edges that would be hidden by joining the index relation.
			runA(definitionCount(`SELECT count(*) FROM pg_depend d JOIN pg_opclass c ON c.oid=d.refobjid WHERE d.classid='pg_class'::regclass AND d.refclassid='pg_opclass'::regclass AND c.opcmethod=$1::oid`, 0, method))
			runA(definitionCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_class'::regclass AND refclassid='pg_class'::regclass AND refobjid=$1::oid`, 0, table))
		}
		noEffects()
		for _, name := range []string{"first", "second"} {
			runA(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_exact_`+name+` USING tx_exact_am`, 0))
		}
		runA(fixture.Exec(`BEGIN`, 0))
		runC(fixture.Exec(`BEGIN`, 0))
		runA(fixture.Exec(`CREATE OPERATOR CLASS public.tx_exact_first DEFAULT FOR TYPE public.tx_exact_label USING tx_exact_am FAMILY public.tx_exact_first AS STORAGE public.tx_exact_label`, 0))
		runC(fixture.Exec(`CREATE OPERATOR CLASS public.tx_exact_second DEFAULT FOR TYPE public.tx_exact_label USING tx_exact_am FAMILY public.tx_exact_second AS STORAGE public.tx_exact_label`, 0))
		first := definitionID(t, p, a, `SELECT oid::bigint FROM pg_opclass WHERE opcname='tx_exact_first'`)
		second := definitionID(t, p, c, `SELECT oid::bigint FROM pg_opclass WHERE opcname='tx_exact_second'`)
		if first == second {
			t.Fatal("independent classes share an identity", first)
		}
		runA(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_exact_first','tx_exact_second')`, 1))
		runC(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_exact_first','tx_exact_second')`, 1))
		definitionRun(t, p, b, b.DB)(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_exact_first','tx_exact_second')`, 0))
		runA(fixture.Exec(`COMMIT`, 0))
		runC(fixture.Exec(`COMMIT`, 0))
		classes := fixture.Select(`SELECT c.oid::bigint,c.opcname,c.opcmethod::bigint,c.opcintype::bigint,c.opcdefault,f.opfname FROM pg_opclass c JOIN pg_opfamily f ON f.oid=c.opcfamily WHERE c.opcname IN ('tx_exact_first','tx_exact_second') ORDER BY c.opcname`, []string{"oid", "opcname", "opcmethod", "opcintype", "opcdefault", "opfname"}, []string{"INT8", "NAME", "INT8", "INT8", "BOOL", "NAME"}, [][]any{{first, "tx_exact_first", method, input, true, "tx_exact_first"}, {second, "tx_exact_second", method, input, true, "tx_exact_second"}})
		runA(classes)
		for _, visibility := range []struct {
			path, name string
		}{
			{"public, pg_catalog", "tx_exact_label"},
			{"pg_catalog", "public.tx_exact_label"},
			{"public, pg_catalog", "tx_exact_label"},
		} {
			runA(fixture.Exec(`SET search_path TO `+visibility.path, 0))
			for i, query := range queries {
				for _, prepared := range []bool{false, true} {
					// ImplicitError asserts both ERROR severities and every exposed
					// field, including absent Detail/Hint/positions/object/context.
					failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42710", Message: `there are multiple default operator classes for data type ` + visibility.name})
					failure.ErrorStage = "execute"
					if prepared {
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, failure)
					} else {
						runA(failure)
					}
					noEffects()
					runA(classes)
				}
			}
		}
	})
}
