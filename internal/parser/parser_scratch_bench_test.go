package parser

import (
	"context"
	"strings"
	"testing"
)

// One mixed operation is one complete cycle: nine SQL successes, four type
// successes, one SQL syntax failure and one full-input type syntax failure.
// Each nested INSERT may invoke more than one generated parse.
func BenchmarkParseScratchMixed(b *testing.B) {
	ctx := context.Background()
	standard := Options{Context: ctx, StandardConformingStrings: true}
	legacy := Options{Context: ctx, StandardConformingStrings: false}
	sqlCases := []struct {
		text    string
		options Options
	}{
		{`SELECT $1,$2,$1 FROM t WHERE id=$1`, standard},
		{`CREATE TABLE bench(a numeric(7,2),b varchar(9))`, standard},
		{`INSERT INTO bench VALUES(1,'first'),(2,E'c\n')`, standard},
		{`INSERT INTO bench ((VALUES($1,'first'),($2,'second') ORDER BY 1 LIMIT 2))`, standard},
		{`SELECT 1 UNION ALL SELECT 2 INTERSECT SELECT 2`, standard},
		{`INSERT INTO bench VALUES(U&'\0061',E'c\n')`, standard},
		{`INSERT INTO bench VALUES('a\nb')`, legacy},
		{`INSERT INTO bench VALUES('a\nb')`, standard},
		{"SELECT " + strings.Repeat("(", 64) + "1" + strings.Repeat(")", 64), standard},
	}
	typeCases := []struct{ input, name string }{
		{"integer", "int4"}, {`"Odd.Schema"."Odd.Type"[]`, "Odd.Type"},
		{"numeric(7,2)[]", "numeric"}, {"character varying(19)", "varchar"},
	}
	cycle := func(b *testing.B) {
		for _, c := range sqlCases {
			statements, err := ParseWithOptions(c.text, c.options)
			if err != nil || len(statements) != 1 {
				b.Fatalf("SQL %q: statements=%d error=%v", c.text, len(statements), err)
			}
		}
		for _, c := range typeCases {
			typ, err := ParseTypeName(ctx, c.input, nil)
			if err != nil || typ.Name != c.name {
				b.Fatalf("type %q: name=%q error=%v", c.input, typ.Name, err)
			}
		}
		if statements, err := ParseWithOptions("SELECT (", standard); err == nil || statements != nil {
			b.Fatalf("invalid SQL published results: %v %v", statements, err)
		}
		if typ, err := ParseTypeName(ctx, "integer trailing", nil); err == nil || typ.Name != "" {
			b.Fatalf("invalid type published results: %v %v", typ, err)
		}
	}
	b.Run("Serial", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			cycle(b)
		}
	})
	b.Run("Parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				cycle(b)
			}
		})
	})
}

// One operation is one ordinary restricted multirow INSERT: outer receiver and
// nested VALUES replay are active simultaneously. This isolates cache-capacity
// pressure without assuming a Get hit, changing GC, or asserting a time bound.
func BenchmarkParseScratchNested(b *testing.B) {
	options := Options{Context: context.Background(), StandardConformingStrings: true}
	const input = `INSERT INTO bench VALUES(1,'first'),(2,E'c\n')`
	parse := func(b *testing.B) {
		statements, err := ParseWithOptions(input, options)
		if err != nil || len(statements) != 1 {
			b.Fatalf("nested INSERT: %v %v", statements, err)
		}
		insert, ok := statements[0].(*Insert)
		if !ok || len(insert.Rows) != 2 {
			b.Fatalf("nested INSERT rows: %v", statements)
		}
	}
	b.Run("Serial", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			parse(b)
		}
	})
	b.Run("Parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				parse(b)
			}
		})
	})
}
