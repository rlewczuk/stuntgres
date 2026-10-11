package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLimitIntegerSlots(t *testing.T) {
	p := referenceLimitFixture(t)
	p.Step(t, fixture.Exec("CREATE TABLE widths(i2 SMALLINT,i4 INTEGER,i8 BIGINT,o OID)", 0))
	p.Step(t, fixture.Exec("INSERT INTO widths VALUES(2,2,2,2)", 1))
	for _, column := range []string{"i2", "i4", "i8", "o"} {
		t.Run(column, func(t *testing.T) {
			q := "SELECT t.id FROM t,widths WHERE widths." + column + "=$1 ORDER BY t.id LIMIT $1"
			step := fixture.Select(q, []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2), int64(2))
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
		})
	}
}
