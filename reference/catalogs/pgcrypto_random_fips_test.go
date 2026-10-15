package reference_test

import (
	"crypto/fips140"
	"database/sql"
	"os"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func pgcryptoRandomFIPSExpected(pg bool) bool {
	if pg {
		return false
	} // Ordinary pinned PostgreSQL18.0 OpenSSL mode, confirmed by the root oracle.
	return fips140.Enabled()
}

func pgcryptoRandomFIPS(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	want := pgcryptoRandomFIPSExpected(pg)
	if !pg {
		for _, option := range strings.Split(os.Getenv("GODEBUG"), ",") {
			if option == "fips140=on" && !want || option == "fips140=off" && want {
				t.Fatal("startup FIPS mode did not match explicit process setting", option, want)
			}
		}
	}
	q := "SELECT pgcrypto_part.fips_mode() AS value"
	s := fixture.Select(q, []string{"value"}, []string{"BOOL"}, [][]any{{want}})
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		b, ok := value.(bool)
		if !ok || b != want {
			t.Fatalf("actual FIPS raw %T %v, want %v", value, value, want)
		}
		return true
	}
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 3 {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, s)
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE pgcrypto_random_fips(v boolean DEFAULT pgcrypto_part.fips_mode())", 0))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_random_fips DEFAULT VALUES", 1))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE VIEW pgcrypto_random_fips_view AS SELECT pgcrypto_part.fips_mode() AS value", 0))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT v FROM pgcrypto_random_fips", []string{"v"}, []string{"BOOL"}, [][]any{{want}}))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT value FROM pgcrypto_random_fips_view", []string{"value"}, []string{"BOOL"}, [][]any{{want}}))
}

func TestReferencePgcryptoRandomFIPSMode(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		pgcryptoRandomInstall(t, p, c, r)
		pgcryptoRandomFIPS(t, p, c, r, pg)
	})
}
