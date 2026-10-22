package parser

import "testing"

func TestVacuumGrammar(t *testing.T) {
	for _, q := range []string{"VACUUM", "VACUUM;", "VACUUM FREEZE VERBOSE t", "VACUUM FULL FREEZE VERBOSE ANALYZE t(id)", "VACUUM (FREEZE false,FREEZE true,PARALLEL -1) ONLY s.t *(id),u"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatalf("%s: %v %v", q, statements, err)
		}
		if _, ok := statements[0].(*Vacuum); !ok {
			t.Fatalf("%s: %T", q, statements[0])
		}
	}
	for _, q := range []string{"CREATE TABLE vacuum(vacuum int)", "SELECT 1 AS vacuum"} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatalf("unreserved VACUUM name lost: %s: %v %v", q, statements, err)
		}
	}
	statements, err := Parse("/*é*/ VACUUM (FREEZE false,FREEZE true) ONLY s.t *(id)")
	if err != nil {
		t.Fatal(err)
	}
	v := statements[0].(*Vacuum)
	if v.Pos.Character != 7 || len(v.Options) != 2 || !v.Options[0].HasValue || v.Options[0].Value.Boolean || !v.Options[1].Value.Boolean || len(v.Targets) != 1 || !v.Targets[0].Only || !v.Targets[0].Star || len(v.Targets[0].Columns) != 1 || v.Targets[0].Columns[0].Text != "id" {
		t.Fatalf("position/ordered values/selection %#v", v)
	}
	for _, q := range []string{"VACUUM VERBOSE FREEZE t", "VACUUM t suffix", "VACUUM (FREEZE) t; VACUUM t suffix", "PREPARE p AS VACUUM t", "CREATE VACUUM"} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatalf("accepted malformed whole input %q %#v %v", q, statements, err)
		}
	}
}
