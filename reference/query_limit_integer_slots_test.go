package reference_test

import "testing"

func TestReferenceQueryLimitIntegerSlots(t *testing.T) {
	p := referenceLimitFixture(t)
	p.step(t, referenceExec("CREATE TABLE widths(i2 SMALLINT,i4 INTEGER,i8 BIGINT,o OID)", 0))
	p.step(t, referenceExec("INSERT INTO widths VALUES(2,2,2,2)", 1))
	for _, column := range []string{"i2", "i4", "i8", "o"} {
		t.Run(column, func(t *testing.T) {
			q := "SELECT t.id FROM t,widths WHERE widths." + column + "=$1 ORDER BY t.id LIMIT $1"
			step := referenceSelect(q, []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2), int64(2))
			p.step(t, step)
			p.step(t, referencePrepared(step))
		})
	}
}
