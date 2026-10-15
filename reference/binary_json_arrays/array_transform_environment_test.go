package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func arrayTransformHeldOutputEnvironment(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	// REL_18_0 varlena.c array_to_text_internal invokes each scalar output
	// routine in the current statement environment, even for a held plan.
	for _, tc := range []struct {
		name, q            string
		settings, expected []string
	}{
		{"bytea", "SELECT array_to_string(ARRAY['\\x0001ff'::bytea],'|') AS a FROM at_one", []string{"SET bytea_output='hex'", "SET bytea_output='escape'", "SET bytea_output='hex'"}, []string{"\\x0001ff", "\\000\\001\\377", "\\x0001ff"}},
		{"date", "SELECT array_to_string(ARRAY['2020-02-03'::date],',') AS a FROM at_one", []string{"SET DateStyle='ISO, MDY'", "SET DateStyle='SQL, DMY'", "SET DateStyle='ISO, MDY'"}, []string{"2020-02-03", "03/02/2020", "2020-02-03"}},
	} {
		t.Run("held_environment_"+tc.name, func(t *testing.T) {
			stmt, err := c.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
			for i, setting := range tc.settings {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(setting, 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(tc.q, []string{"a"}, []string{"TEXT"}, [][]any{{tc.expected[i]}}))
			}
		})
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE at_identifiers(a REGCLASS[])", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO at_identifiers VALUES(ARRAY['at_output_renamed'::regclass])", 1))
	const q = "SELECT array_to_string(a,',') AS a FROM at_identifiers"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	check := fixture.Select(q, []string{"a"}, []string{"TEXT"}, [][]any{{"at_output_renamed"}})
	fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
	var old *sql.Rows
	if !b.PG {
		old, err = c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE at_output_renamed RENAME TO at_output_final", 0))
	check.Rows = [][]any{{"at_output_final"}}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
	arrayTransformBoth(t, p, b, c, check)
	if old != nil {
		fixture.ArrayReferenceConsumeHeld(t, old, [][]any{{"at_output_renamed"}}, []string{"TEXT"})
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=pg_catalog", 0))
	qualified := fixture.Select("SELECT array_to_string(a,',') AS a FROM public.at_identifiers", []string{"a"}, []string{"TEXT"}, [][]any{{"public.at_output_final"}})
	arrayTransformBoth(t, p, b, c, qualified)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=public", 0))
}
