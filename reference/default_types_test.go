//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceDefaultDestinationTypes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			run(referenceExec(`CREATE TABLE types(s SMALLINT DEFAULT 32767,l BIGINT DEFAULT 2147483648,u UUID DEFAULT '550e8400-e29b-41d4-a716-446655440000',r REAL DEFAULT 1.25,f DOUBLE PRECISION DEFAULT 2.5,n NUMERIC DEFAULT 'NaN')`, 0))
			run(referenceExec(`INSERT INTO types DEFAULT VALUES`, 1))
			run(referenceSelect(`SELECT * FROM types`, []string{"s", "l", "u", "r", "f", "n"}, []string{"INT2", "INT8", "UUID", "FLOAT4", "FLOAT8", "NUMERIC"}, [][]any{{int64(32767), int64(2147483648), "550e8400-e29b-41d4-a716-446655440000", float64(1.25), float64(2.5), "NaN"}}))
		})
	}
}
