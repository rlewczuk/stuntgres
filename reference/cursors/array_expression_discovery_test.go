package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Stage A PostgreSQL 18.0 observations establish the mixed-target diagnostics,
// eager constant-error phase, domain access and retained occurrence typmod.
func TestReferenceArrayExpressionOracleQuestions(t *testing.T) {
	for _, tc := range []struct {
		name, q string
		want    fixture.Diagnostic
	}{
		{"update_whole_partial", "UPDATE public.ax SET a='{7}',a[1]=8", fixture.Diagnostic{Code: "42601", Message: `multiple assignments to same column "a"`}},
		{"update_partial_whole", "UPDATE public.ax SET a[1]=8,a='{7}'", fixture.Diagnostic{Code: "42601", Message: `multiple assignments to same column "a"`}},
		{"insert_whole_partial", "INSERT INTO public.ax(a,a[1]) VALUES('{7}',8)", fixture.Diagnostic{Code: "42701", Message: `column "a" specified more than once`, Position: "25"}},
		{"insert_partial_whole", "INSERT INTO public.ax(a[1],a) VALUES(8,'{7}')", fixture.Diagnostic{Code: "42701", Message: `column "a" specified more than once`, Position: "28"}},
		{"destination_empty", "INSERT INTO public.ax(a) VALUES(ARRAY[])", fixture.Diagnostic{Code: "42P18", Message: "cannot determine type of empty array", Position: "33", Hint: "Explicitly cast to the desired type, for example ARRAY[]::integer[]."}},
		{"destination_text", "INSERT INTO public.ax(a) VALUES(ARRAY['1','2'])", fixture.Diagnostic{Code: "42804", Message: `column "a" is of type integer[] but expression is of type text[]`, Position: "33", Hint: "You will need to rewrite or cast the expression."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				arrayExpressionFixture(t, p, b, c)
				tc.want.Severity = "ERROR"
				tc.want.SeverityUnlocalized = "ERROR"
				s := fixture.ErrorDiagnostic(tc.q, tc.want)
				s.ErrorStage = "prepare"
				s.Prepared = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		})
	}
	t.Run("empty_source_constant_phase", func(t *testing.T) {
		arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE public.ax_empty (id INT)", 0))
			s := fixture.ErrorDiagnostic("SELECT ARRAY[ARRAY[1],ARRAY[2,3]] FROM public.ax_empty", fixture.ControlsReferenceDiagnostic("2202E", "multidimensional arrays must have array expressions with matching dimensions"))
			s.SelectRows = true
			s.Prepared = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	})
	t.Run("occurrence_metadata", func(t *testing.T) {
		arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE public.ax_meta (a VARCHAR(2)[])", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.ax_meta VALUES ('{ab}')", 1))
			// Stage A observed typmod6 for constructor, element and slice in the
			// stored view. Scalar access exposes that retained VARCHAR(2) modifier
			// through the public ColumnType.Length contract, without catalog probes.
			s := fixture.ArrayReferenceSelect("SELECT ARRAY['a'::varchar(2)] AS c,a[1] AS e,a[:] AS s,(ARRAY['a'::varchar(2)])[1] AS ce,(a[:])[1] AS se FROM public.ax_meta", []string{"c", "e", "s", "ce", "se"}, []string{"_VARCHAR", "VARCHAR", "_VARCHAR", "VARCHAR", "VARCHAR"}, []string{"_VARCHAR", "VARCHAR", "_VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"{a}", "ab", "{ab}", "a", "ab"}}, b.PG)
			base := s.CheckMetadata
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				base(t, r)
				cols, err := r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for _, i := range []int{1, 3, 4} {
					n, known := cols[i].Length()
					if n != 2 || !known {
						t.Fatalf("column %s Length=%d/%t want 2/true", cols[i].Name(), n, known)
					}
				}
			}
			arrayExpressionBoth(t, p, b, c, s)
		})
	})
	t.Run("domain_access", func(t *testing.T) {
		arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN public.ax_container AS INT[]", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE public.ax_domain (a public.ax_container)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.ax_domain VALUES ('{7,8}')", 1))
			arrayExpressionBoth(t, p, b, c, fixture.ArrayReferenceSelect("SELECT a[1] AS e,a[:] AS s FROM public.ax_domain", []string{"e", "s"}, []string{"INT4", "_INT4"}, []string{"INT4", "_INT4"}, [][]any{{int64(7), "{7,8}"}}, b.PG))
		})
	})
}

func TestReferenceArrayExpressionTransactionRecovery(t *testing.T) {
	arrayExpressionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		arrayExpressionFixture(t, p, b, c)
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
		run(fixture.Exec("SAVEPOINT ax_before", 0))
		run(fixture.ErrorDiagnostic("UPDATE public.ax SET a[1:2]=ARRAY[7] WHERE id=1", fixture.ControlsReferenceDiagnostic("2202E", "source array too small")))
		run(fixture.Exec("ROLLBACK TO ax_before", 0))
		run(fixture.Exec("UPDATE public.ax SET a[1]=7 WHERE id=1", 1))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ArrayReferenceSelect("SELECT a FROM public.ax WHERE id=1", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{7,NULL,30}"}}, b.PG))
	})
}
