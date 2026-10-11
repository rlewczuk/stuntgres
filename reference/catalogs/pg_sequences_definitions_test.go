package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGSequencesDefinitions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			e := b.DB
			owner := pgSequencesOwner(t, p, e)
			cases := []struct {
				ddl, name string
				row       []any
			}{
				{"CREATE SEQUENCE pgsv_default", "pgsv_default", pgSequencesDefault(owner, "pgsv_default", nil)},
				{"CREATE SEQUENCE pgsv_custom AS INTEGER INCREMENT 7 MINVALUE -20 MAXVALUE 200 START 15 CACHE 9 CYCLE", "pgsv_custom", []any{"public", "pgsv_custom", owner, "integer", int64(15), int64(-20), int64(200), int64(7), true, int64(9), nil}},
				{"CREATE SEQUENCE pgsv_small AS SMALLINT", "pgsv_small", []any{"public", "pgsv_small", owner, "smallint", int64(1), int64(1), int64(32767), int64(1), false, int64(1), nil}},
				{"CREATE SEQUENCE pgsv_down AS INTEGER INCREMENT -1", "pgsv_down", []any{"public", "pgsv_down", owner, "integer", int64(-1), int64(-2147483648), int64(-1), int64(-1), false, int64(1), nil}},
			}
			for _, c := range cases {
				pgSequenceExec(t, p, b, e, c.ddl)
				pgSequencesCheck(t, p, b, e, c.name, c.row)
			}
			pgSequenceExec(t, p, b, e, "CREATE SEQUENCE pgsv_restart AS INTEGER START 5 RESTART 9 CACHE 3")
			restart := []any{"public", "pgsv_restart", owner, "integer", int64(5), int64(1), int64(2147483647), int64(1), false, int64(3), nil}
			pgSequencesCheck(t, p, b, e, "pgsv_restart", restart)
			pgSequencesScalar(t, p, b, e, "SELECT nextval('pgsv_restart')", "nextval", 9)
			restart[10] = int64(11)
			pgSequencesCheck(t, p, b, e, "pgsv_restart", restart)
			row := cases[1].row
			pgSequenceExec(t, p, b, e, "ALTER SEQUENCE pgsv_custom START 21")
			row[4] = int64(21)
			pgSequencesCheck(t, p, b, e, "pgsv_custom", row)
			pgSequenceExec(t, p, b, e, "ALTER SEQUENCE pgsv_custom AS BIGINT INCREMENT 3 MINVALUE -30 MAXVALUE 300 CACHE 4 NO CYCLE RESTART 27")
			row[3], row[5], row[6], row[7], row[8], row[9] = "bigint", int64(-30), int64(300), int64(3), false, int64(4)
			pgSequencesCheck(t, p, b, e, "pgsv_custom", row)
			bad := fixture.ImplicitError("ALTER SEQUENCE pgsv_custom CACHE 0", fixture.Diagnostic{Code: "22023", Message: "CACHE (0) must be greater than zero"})
			fixture.RunStep(t, p.Ctx, e, b.Recorder, bad)
			pgSequencesCheck(t, p, b, e, "pgsv_custom", row)
			pgSequenceExec(t, p, b, e, "CREATE SCHEMA pgsv_defschema")
			defer e.ExecContext(p.Ctx, "DROP SCHEMA pgsv_defschema CASCADE")
			pgSequenceExec(t, p, b, e, "ALTER SEQUENCE pgsv_custom RENAME TO pgsv_renamed")
			pgSequencesCheck(t, p, b, e, "pgsv_custom", nil)
			row[1] = "pgsv_renamed"
			pgSequenceExec(t, p, b, e, "ALTER SEQUENCE pgsv_renamed SET SCHEMA pgsv_defschema")
			row[0] = "pgsv_defschema"
			pgSequencesCheck(t, p, b, e, "pgsv_renamed", row)
			pgSequenceExec(t, p, b, e, "CREATE SEQUENCE public.pgsv_renamed")
			s := pgSequencesSelect(b, "SELECT * FROM pg_sequences WHERE sequencename='pgsv_renamed'", [][]any{row, pgSequencesDefault(owner, "pgsv_renamed", nil)})
			fixture.PgDescriptionRows(t, p, b, s)
			pgSequenceExec(t, p, b, e, "DROP SEQUENCE public.pgsv_renamed")
			pgSequenceExec(t, p, b, e, "DROP SEQUENCE pgsv_defschema.pgsv_renamed")
			pgSequencesCheck(t, p, b, e, "pgsv_renamed", nil)
			pgSequenceExec(t, p, b, e, "CREATE TABLE pgsv_owned_table(a INT,b INT)")
			for _, q := range []string{"CREATE SEQUENCE pgsv_owned_column OWNED BY pgsv_owned_table.a", "CREATE SEQUENCE pgsv_owned_table_seq OWNED BY pgsv_owned_table.b", "ALTER TABLE pgsv_owned_table DROP COLUMN a"} {
				pgSequenceExec(t, p, b, e, q)
			}
			pgSequencesCheck(t, p, b, e, "pgsv_owned_column", nil)
			pgSequenceExec(t, p, b, e, "DROP TABLE pgsv_owned_table")
			pgSequencesCheck(t, p, b, e, "pgsv_owned_table_seq", nil)
		})
	}
}
