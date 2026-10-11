//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLikeParameterTypesOracle(t *testing.T) {
	p := fixture.NewPair(t)
	fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("CREATE TABLE t(n NAME,c CHAR(3),v VARCHAR(3),s TEXT,b BYTEA)", 0))
	// Named PREPARE exposes PostgreSQL's selected SQL parameter types;
	// this is an oracle-only catalog witness, not native dialect expansion.
	conn, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{
		"SELECT n LIKE $1 FROM t", "SELECT $1 LIKE n FROM t",
		"SELECT c LIKE $1 FROM t", "SELECT $1 LIKE c FROM t",
		"SELECT v LIKE $1 FROM t", "SELECT $1 LIKE v FROM t",
		"SELECT $1,s LIKE $1 FROM t", "SELECT s LIKE $1,$1 FROM t",
	} {
		if _, err := conn.ExecContext(p.Ctx, "PREPARE like_gate AS "+q); err != nil {
			t.Fatal(q, err)
		}
		fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, fixture.Select("SELECT parameter_types::text AS types FROM pg_prepared_statements WHERE name='like_gate'", []string{"types"}, []string{"TEXT"}, [][]any{{"{text}"}}))
		if _, err := conn.ExecContext(p.Ctx, "DEALLOCATE like_gate"); err != nil {
			t.Fatal(err)
		}
	}
}
