package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstathashindexBinding(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN ph_part.ph_oid AS regclass", 0))
			for _, arg := range []string{"'ph_part.ph_index'", "'ph_part.ph_index'::text", "'ph_part.ph_index'::regclass", "'ph_part.ph_index'::ph_part.ph_oid", "relname => 'ph_part.ph_index'"} {
				pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex("+arg+")", 0, 0)
			}
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ph_part.ph_index'::regclass::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			for _, typ := range []string{"oid", "integer", "bigint"} {
				pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex($1::"+typ+")", 0, 0, oid)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=ph_part,pg_catalog", 0))
			pgstathashindexStats(t, p, b, c, "SELECT * FROM pgstathashindex('ph_part.ph_index')", 0, 0)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION public.pgstathashindex(regclass) RETURNS integer LANGUAGE SQL AS 'SELECT 77'", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=public,ph_part,pg_catalog", 0))
			pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT pgstathashindex('ph_part.ph_index'::regclass) AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(77)}}))
			pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex('ph_part.ph_index')", 0, 0)
		})
	})
	for _, tc := range []struct{ name, arg, signature string }{
		{"boolean", "true", "ph_part.pgstathashindex(boolean)"},
		{"zero_arguments", "", "ph_part.pgstathashindex()"},
		{"two_arguments", "NULL::regclass,NULL::regclass", "ph_part.pgstathashindex(regclass, regclass)"},
		{"wrong_argument_name", "reloid => NULL::regclass", "ph_part.pgstathashindex(reloid => regclass)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "SELECT * FROM ph_part.pgstathashindex(" + tc.arg + ")"
				d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
				d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				d.Position = "15"
				s := fixture.ImplicitError(q, d)
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				st, err := c.PrepareContext(p.Ctx, q)
				if st != nil {
					st.Close()
					t.Fatal("invalid signature prepared successfully")
				}
				fixture.AssertDiagnostic(t, err, &d)
				pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex('ph_part.ph_index')", 0, 0)
			})
		})
	}
}
