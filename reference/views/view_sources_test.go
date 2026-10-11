//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewSequenceCatalogPrivateVisibility(t *testing.T) {
	p := fixture.NewPair(t)
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE SEQUENCE s", 0),
		fixture.Exec("CREATE VIEW sequence_view AS SELECT last_value FROM s", 0),
		fixture.Exec("BEGIN", 0),
		fixture.Exec("DROP SEQUENCE s CASCADE", 0),
		fixture.Select("SELECT viewname FROM pg_catalog.pg_views WHERE viewname='sequence_view'", []string{"viewname"}, []string{"NAME"}, nil),
		fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='sequence_view'", []string{"relname"}, []string{"NAME"}, nil),
		fixture.Exec("ROLLBACK", 0),
		fixture.Select("SELECT viewname FROM pg_catalog.pg_views WHERE viewname='sequence_view'", []string{"viewname"}, []string{"NAME"}, [][]any{{"sequence_view"}}),
	} {
		p.Step(t, step)
	}
}
