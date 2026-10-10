package parser

import "testing"

func TestMergeInsertSyntax(t *testing.T) {
	queries := []string{
		"MERGE INTO public.t AS dst USING s AS src ON dst.id=src.id WHEN NOT MATCHED BY TARGET AND src.id>0 THEN INSERT(v,id) OVERRIDING SYSTEM VALUE VALUES(src.v,src.id) WHEN NOT MATCHED THEN INSERT DEFAULT VALUES",
		"MERGE INTO merge USING matched ON merge.source=matched.source WHEN NOT MATCHED THEN INSERT(target,nothing) OVERRIDING USER VALUE VALUES(1,DEFAULT)",
		"MERGE INTO t USING (VALUES(1)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES((SELECT s.id))",
		"MERGE INTO t USING s JOIN r USING(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(id)",
	}
	for _, q := range queries {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		m, ok := statements[0].(*Merge)
		if !ok || m.Pos.Character != 1 || m.Source == nil || m.On == nil || len(m.Arms) == 0 {
			t.Fatal(q, statements)
		}
		for _, arm := range m.Arms {
			if arm.Pos.Character == 0 || arm.ActionPos.Character == 0 {
				t.Fatal("missing clause/action positions", arm)
			}
			for _, target := range arm.Targets {
				if target.Pos.Character == 0 {
					t.Fatal(target)
				}
			}
			for _, value := range arm.Values {
				if value.SourcePosition().Character == 0 {
					t.Fatal(value)
				}
			}
		}
	}
	statements, err := Parse(queries[0])
	if err != nil {
		t.Fatal(err)
	}
	m := statements[0].(*Merge)
	if len(m.Arms) != 2 || m.Arms[0].Condition == nil || len(m.Arms[0].Targets) != 2 || m.Arms[0].Targets[0].Name != "v" || m.Arms[0].Override != OverrideSystem || !m.Arms[1].DefaultValues || len(m.Arms[1].Values) != 0 {
		t.Fatal(m.Arms)
	}
}

func TestMergeInsertRejectsFullInput(t *testing.T) {
	for _, suffix := range []string{
		"WHEN MATCHED THEN UPDATE SET id=1",
		"WHEN MATCHED THEN DELETE",
		"WHEN NOT MATCHED THEN DO NOTHING",
		"WHEN NOT MATCHED BY SOURCE THEN DELETE",
		"WHEN NOT MATCHED THEN INSERT INTO t VALUES(1)",
		"WHEN NOT MATCHED THEN INSERT SELECT 1",
		"WHEN NOT MATCHED THEN INSERT VALUES(1),(2)",
		"WHEN NOT MATCHED THEN INSERT VALUES()",
		"WHEN NOT MATCHED THEN INSERT VALUES(1) RETURNING id",
		"WHEN NOT MATCHED THEN INSERT DEFAULT VALUES trailing",
		"WHEN NOT MATCHED THEN INSERT VALUES(1); garbage",
		"",
	} {
		q := "MERGE INTO t USING s ON FALSE " + suffix
		statements, err := Parse(q)
		if err == nil || len(statements) != 0 {
			t.Fatalf("invalid or excluded full input accepted: %q %v %v", q, statements, err)
		}
	}
}

func TestMergeInsertQueryVisitor(t *testing.T) {
	q := "MERGE INTO t USING s ON EXISTS(SELECT 1 FROM a) WHEN NOT MATCHED AND EXISTS(SELECT 1 FROM b) THEN INSERT VALUES((SELECT id FROM c)) WHEN NOT MATCHED THEN INSERT DEFAULT VALUES"
	statements, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	var seen []*TableReference
	err = VisitQuerySources(statements[0], nil, func(table *TableReference, _ *Select) error { seen = append(seen, table); return nil })
	if err != nil || len(seen) != 4 {
		t.Fatal("source/ON/condition/value lexical order", seen, err)
	}
}
