//go:build reference

package reference_test

import "testing"

func TestReferenceLikeParameterTypesOracle(t *testing.T) {
	p := newReferencePair(t)
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("CREATE TABLE t(n NAME,c CHAR(3),v VARCHAR(3),s TEXT,b BYTEA)", 0))
	// Named PREPARE exposes PostgreSQL's selected SQL parameter types;
	// this is an oracle-only catalog witness, not native dialect expansion.
	conn, err := p.postgres.Conn(p.ctx)
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
		if _, err := conn.ExecContext(p.ctx, "PREPARE like_gate AS "+q); err != nil {
			t.Fatal(q, err)
		}
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, referenceSelect("SELECT parameter_types::text AS types FROM pg_prepared_statements WHERE name='like_gate'", []string{"types"}, []string{"TEXT"}, [][]any{{"{text}"}}))
		if _, err := conn.ExecContext(p.ctx, "DEALLOCATE like_gate"); err != nil {
			t.Fatal(err)
		}
	}
}
