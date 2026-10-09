//go:build reference

package reference_test

import (
	"testing"
)

func TestReferenceViewSequenceCatalogPrivateVisibility(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE SEQUENCE s", 0),
		referenceExec("CREATE VIEW sequence_view AS SELECT last_value FROM s", 0),
		referenceExec("BEGIN", 0),
		referenceExec("DROP SEQUENCE s CASCADE", 0),
		referenceSelect("SELECT viewname FROM pg_catalog.pg_views WHERE viewname='sequence_view'", []string{"viewname"}, []string{"NAME"}, nil),
		referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='sequence_view'", []string{"relname"}, []string{"NAME"}, nil),
		referenceExec("ROLLBACK", 0),
		referenceSelect("SELECT viewname FROM pg_catalog.pg_views WHERE viewname='sequence_view'", []string{"viewname"}, []string{"NAME"}, [][]any{{"sequence_view"}}),
	} {
		p.step(t, step)
	}
}
