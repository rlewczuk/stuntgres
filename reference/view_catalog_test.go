//go:build reference

package reference_test

import "testing"

func TestReferenceViewCatalogInputs(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT)", 0),
		referenceExec("CREATE VIEW v AS SELECT id FROM t", 0),
		referencePrepared(referenceSelect("SELECT rulename FROM pg_catalog.pg_rewrite WHERE ev_class=$1", []string{"rulename"}, []string{"NAME"}, nil, "-1")),
		referencePrepared(referenceSelect("SELECT rulename FROM pg_catalog.pg_rewrite WHERE ev_class=$1", []string{"rulename"}, []string{"NAME"}, nil, "-2147483648")),
		referencePrepared(referenceSelect("SELECT relkind FROM pg_catalog.pg_class WHERE relname='v' AND relkind=$1", []string{"relkind"}, []string{"CHAR"}, [][]any{{"v"}}, "vv")),
		referencePrepared(referenceSelect("SELECT relkind FROM pg_catalog.pg_class WHERE relname='v' AND relkind=$1", []string{"relkind"}, []string{"CHAR"}, [][]any{{"v"}}, `\166`)),
		referencePrepared(referenceSelect("SELECT relkind FROM pg_catalog.pg_class WHERE relname='v' AND relkind<$1", []string{"relkind"}, []string{"CHAR"}, [][]any{{"v"}}, "é")),
	} {
		p.step(t, step)
	}
}
