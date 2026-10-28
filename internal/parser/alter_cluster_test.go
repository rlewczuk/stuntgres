package parser

import (
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestAlterClusterGrammar(t *testing.T) {
	for _, target := range []string{"t", "ONLY t", "ONLY(t)", "t *", "IF EXISTS ONLY t"} {
		statements, err := Parse("ALTER TABLE " + target + ` CLUSTER ON "My index", SET WITHOUT CLUSTER, SET WITHOUT OIDS`)
		if err != nil {
			t.Fatal(target, err)
		}
		a, ok := statements[0].(*AlterTable)
		if !ok || len(a.Actions) != 3 || a.Actions[0].Action != ClusterOn || a.Actions[0].ClusterIndex.Text != "My index" || a.Actions[0].ClusterIndex.Pos.Character <= a.Actions[0].Pos.Character || a.Actions[1].Action != SetWithoutCluster || a.Actions[2].Action != SetWithoutOIDs || a.Actions[1].Pos.Character >= a.Actions[2].Pos.Character {
			t.Fatal(target, statements)
		}
	}
	for _, q := range []string{"ALTER MATERIALIZED VIEW t CLUSTER ON i", "ALTER MATERIALIZED VIEW IF EXISTS t SET WITHOUT CLUSTER", "CREATE TABLE cluster(oids int)", "ALTER TABLE cluster CLUSTER ON oids"} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{"ALTER MATERIALIZED VIEW t DEPENDS ON EXTENSION ext", "ALTER MATERIALIZED VIEW t NO DEPENDS ON EXTENSION ext"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		a, ok := statements[0].(*AlterView)
		if !ok || !a.Materialized || a.Target.Text != "ext" {
			t.Fatal(q, statements)
		}
	}
	for _, q := range []string{"ALTER TABLE t CLUSTER ON public.i", "ALTER TABLE t CLUSTER ON i junk", "ALTER TABLE t CLUSTER ON", "ALTER TABLE t SET WITHOUT", "ALTER TABLE t SET WITHOUT \"cluster\"", "ALTER TABLE t \"cluster\" ON i"} {
		if _, err := Parse(q); err == nil {
			t.Fatal("accepted malformed full input", q)
		}
	}
	for _, q := range []string{"ALTER TABLE t SET WITH OIDS", "/*é*/ ALTER TABLE t SET WITH OIDS"} {
		_, err := Parse(q)
		e, ok := err.(*sqlstate.Error)
		if !ok || e.Code != "42601" || e.Message != `syntax error at or near "WITH"` {
			t.Fatal(q, err)
		}
		want := "19"
		if q[0] == '/' {
			want = "25"
		}
		if e.Position != want {
			t.Fatal("lost character position", q, e.Position, want)
		}
	}
}
