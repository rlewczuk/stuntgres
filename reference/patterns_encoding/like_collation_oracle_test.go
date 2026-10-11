//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLikeViewCollationOracle(t *testing.T) {
	p := fixture.NewPair(t)
	for _, s := range []fixture.Step{fixture.Exec(`CREATE TABLE t(p TEXT COLLATE "POSIX")`, 0), fixture.Exec("CREATE VIEW cast_pattern AS SELECT CAST(p AS TEXT) AS pattern FROM t", 0), fixture.Select("SELECT attcollation::bigint AS collation_oid FROM pg_attribute WHERE attrelid='cast_pattern'::regclass AND attname='pattern'", []string{"collation_oid"}, []string{"INT8"}, [][]any{{int64(951)}})} {
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
	}
}
