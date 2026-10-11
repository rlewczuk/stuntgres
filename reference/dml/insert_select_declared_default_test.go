//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectDeclaredDefault(t *testing.T) {
	p := fixture.NewPair(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(i INT,j INT DEFAULT 42)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t(i) SELECT 1", 1))
	p.Step(t, fixture.Select("SELECT * FROM t", []string{"i", "j"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(42)}}))
}
