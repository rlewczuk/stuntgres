package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BenchmarkPLpgSQLWalk measures one compiled invocation per op. Comparing the
// loop sizes separates per-invocation from per-iteration allocations.
func BenchmarkPLpgSQLWalk(b *testing.B) {
	for _, tc := range []struct{ name, source string }{
		{"return", "BEGIN RETURN 1; END"},
		{"loop_0", "DECLARE n integer:=0; BEGIN LOOP EXIT WHEN n>=0; n:=n+1; END LOOP; RETURN n; END"},
		{"loop_64", "DECLARE n integer:=0; BEGIN LOOP EXIT WHEN n>=64; IF n<0 THEN RETURN 0; END IF; n:=n+1; END LOOP; RETURN n; END"},
		{"query_for_1", "DECLARE r record; n integer:=0; BEGIN FOR r IN SELECT 1 AS i LOOP n:=n+r.i; END LOOP; RETURN n; END"},
		{"query_for_8", "DECLARE r record; n integer:=0; BEGIN FOR r IN SELECT g AS i FROM generate_series(1,8) g LOOP n:=n+r.i; END LOOP; RETURN n; END"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			s, d := plWalkBenchmarkRoutine(b, tc.source)
			read := s.Engine().CatalogRead()
			input := proc.NewFrame(nil)
			defer input.Close()
			if _, err := s.invokePLRoutine(d, input, read, sqltype.CollationRef{}); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := s.invokePLRoutine(d, input, read, sqltype.CollationRef{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func plWalkBenchmarkRoutine(b *testing.B, source string) (*StatementScope, *catalog.Procedure) {
	b.Helper()
	e := New()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(done)
	s, err := e.StartStatement(op)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Finish(nil) })
	read := e.CatalogRead()
	integer, _ := read.LookupType(sqltype.Int4)
	language, err := catalog.LookupLanguage(read, "plpgsql")
	if err != nil {
		b.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		b.Fatal(err)
	}
	d := &catalog.Procedure{ObjectName: "walk_bench", Language: language.Address(), SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Name: "walk_bench", Source: source, Result: integer.Usage(), PLSyntax: raw}}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err == nil {
		err = e.CompilePLRoutineLocked(s.Op, auth, d)
	}
	e.Mu.Unlock()
	if err != nil {
		b.Fatal(err)
	}
	return s, d
}
