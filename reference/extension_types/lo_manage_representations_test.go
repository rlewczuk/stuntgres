package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations follow SPI_getvalue string comparison and decimal atooid in
// REL_18_6 lo.c/postgres_ext.h, independently of native scalar coercion.
func TestReferenceLoManageRepresentations(t *testing.T) {
	for _, tc := range []struct {
		name, typ, old, mutation string
		removed                  int64
	}{
		{"integer_update", "integer", "910201", "UPDATE lm SET v=910202", 910201},
		{"integer_delete", "integer", "910201", "DELETE FROM lm", 910201},
		{"text_spelling_change", "text", "0910201", "UPDATE lm SET v='910201'", 910201},
		{"text_decimal_prefix", "text", "  +910201suffix", "DELETE FROM lm", 910201},
		{"text_unsigned_narrowing", "text", "4295877497", "DELETE FROM lm", 910201},
		{"text_negative", "text", "-1", "DELETE FROM lm", 4294967295},
		{"text_overflow", "text", "18446744073709551616", "DELETE FROM lm", 4294967295},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				lmSetup(t, p, b, c, tc.typ, "v", "UPDATE OR DELETE", "ROW")
				loRun(t, p, b, c, fixture.Exec("DROP TRIGGER lm_cleanup ON lm", 0))
				loRun(t, p, b, c, fixture.Exec("UPDATE lm SET v='"+tc.old+"'", 1))
				loRun(t, p, b, c, fixture.Exec("CREATE TRIGGER lm_cleanup BEFORE UPDATE OR DELETE ON lm FOR EACH ROW EXECUTE FUNCTION lo_manage(v)", 0))
				if tc.removed == 4294967295 {
					lmCreate(t, p, b, c, tc.removed, "fe")
				}
				// Equal scalar output must not attempt unlink even for non-OID types.
				lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=v", 1))
				if tc.removed == 4294967295 {
					lmBytes(t, p, b, c, tc.removed, "fe")
				} else {
					lmBytes(t, p, b, c, tc.removed, "00ff")
				}
				lmRun(t, p, b, c, prepared, fixture.Exec(tc.mutation, 1))
				lmMissing(t, p, b, c, tc.removed)
				lmBytes(t, p, b, c, 910202, "6162")
				want := [][]any{}
				typeName := "TEXT"
				if tc.typ == "integer" {
					typeName = "INT4"
				}
				if tc.mutation == "UPDATE lm SET v=910202" {
					want = [][]any{{int64(1), int64(910202), int64(0)}}
				}
				if tc.mutation == "UPDATE lm SET v='910201'" {
					want = [][]any{{int64(1), "910201", int64(0)}}
				}
				loRun(t, p, b, c, fixture.Select("SELECT id,v,n FROM lm ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", typeName, "INT4"}, want))
			})
		})
	}
	t.Run("text_no_digits", func(t *testing.T) {
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			lmSetup(t, p, b, c, "text", "v", "UPDATE OR DELETE", "ROW")
			loRun(t, p, b, c, fixture.Exec("DROP TRIGGER lm_cleanup ON lm", 0))
			loRun(t, p, b, c, fixture.Exec("UPDATE lm SET v='junk'", 1))
			loRun(t, p, b, c, fixture.Exec("CREATE TRIGGER lm_cleanup BEFORE DELETE ON lm FOR EACH ROW EXECUTE FUNCTION lo_manage(v)", 0))
			lmRun(t, p, b, c, prepared, lmError("DELETE FROM lm", "42704", "large object 0 does not exist"))
			loRun(t, p, b, c, fixture.Select("SELECT id,v,n FROM lm ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "junk", int64(0)}}))
			lmBytes(t, p, b, c, 910201, "00ff")
		})
	})
}
