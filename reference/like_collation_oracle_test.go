//go:build reference

package reference_test

import "testing"

func TestReferenceLikeViewCollationOracle(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{referenceExec(`CREATE TABLE t(p TEXT COLLATE "POSIX")`, 0), referenceExec("CREATE VIEW cast_pattern AS SELECT CAST(p AS TEXT) AS pattern FROM t", 0), referenceSelect("SELECT attcollation::bigint AS collation_oid FROM pg_attribute WHERE attrelid='cast_pattern'::regclass AND attname='pattern'", []string{"collation_oid"}, []string{"INT8"}, [][]any{{int64(951)}})} {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
	}
}
