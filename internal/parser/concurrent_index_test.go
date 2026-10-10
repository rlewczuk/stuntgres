package parser

import "testing"

func TestConcurrentIndexHeads(t *testing.T) {
	for _, tc := range []struct {
		query        string
		unique, skip bool
		name         string
		position     int
	}{
		{"CREATE INDEX CONCURRENTLY ON t(n)", false, false, "", 14},
		{"CREATE INDEX CONCURRENTLY ix ON t(n)", false, false, "ix", 14},
		{"CREATE INDEX CONCURRENTLY IF NOT EXISTS ix ON t(n)", false, true, "ix", 14},
		{"CREATE UNIQUE INDEX CONCURRENTLY ON t(n)", true, false, "", 21},
		{"CREATE UNIQUE INDEX CONCURRENTLY ix ON t(n)", true, false, "ix", 21},
		{"CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS ix ON t(n)", true, true, "ix", 21},
	} {
		statements, err := Parse(tc.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.query, statements, err)
		}
		index, ok := statements[0].(*CreateIndex)
		if !ok || index.Unique != tc.unique || index.IfNotExists != tc.skip || index.Name.Text != tc.name || index.Concurrent.Character != tc.position || index.Pos.Character != 1 || index.Table.Parts[0].Text != "t" || len(index.Keys) != 1 {
			t.Fatalf("%s: %#v", tc.query, index)
		}
	}
	statements, err := Parse(`CREATE UNIQUE INDEX CONCURRENTLY "é" ON public.t(lower(v) COLLATE "C" text_ops DESC NULLS LAST,(n+1),n,n) INCLUDE(payload)`)
	if err != nil {
		t.Fatal(err)
	}
	index := statements[0].(*CreateIndex)
	if !index.Unique || len(index.Keys) != 4 || index.Keys[0].Expression == nil || index.Keys[1].Expression == nil || index.Keys[0].Collation.Parts[0].Text != "C" || index.Keys[0].Opclass.Parts[0].Text != "text_ops" || !index.Keys[0].Desc || index.Keys[0].Nulls != NullsLast || len(index.Include) != 1 {
		t.Fatal(index)
	}
}

func TestConcurrentIndexOrdinaryParseAuthority(t *testing.T) {
	query := `CREATE UNIQUE INDEX ui ON public.t(lower(v) COLLATE "C" text_ops DESC NULLS LAST) INCLUDE(payload) NULLS NOT DISTINCT`
	statements, err := Parse(query)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	index := statements[0].(*CreateIndex)
	if !index.Unique || !index.NullsNotDistinct || index.Concurrent.Character != 0 || index.Name.Text != "ui" || len(index.Include) != 1 {
		t.Fatal(index)
	}
	statements, err = Parse("CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n) WHERE n>0")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	index = statements[0].(*CreateIndex)
	if !index.Unique || index.Concurrent.Character != 21 || index.Predicate == nil {
		t.Fatal(index)
	}
}
