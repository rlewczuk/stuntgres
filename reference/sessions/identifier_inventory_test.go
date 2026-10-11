//go:build reference

package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentifierKeywordInventory(t *testing.T) {
	p := fixture.NewPair(t)
	rows, err := p.Postgres.QueryContext(p.Ctx, "SELECT word,catcode,barelabel,catdesc,baredesc FROM pg_get_keywords() ORDER BY word")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	total, bareCount := 0, 0
	previous := ""
	samples := map[string][]any{}
	descriptions := map[string]string{"U": "unreserved", "C": "unreserved (cannot be function or type name)", "T": "reserved (can be function or type name)", "R": "reserved"}
	for rows.Next() {
		var word, cat, desc, baredesc string
		var bare bool
		if err := rows.Scan(&word, &cat, &bare, &desc, &baredesc); err != nil {
			t.Fatal(err)
		}
		if word <= previous || descriptions[cat] != desc {
			t.Fatalf("inventory %s %s %s", word, cat, desc)
		}
		previous = word
		wantBare := "requires AS"
		if bare {
			wantBare = "can be bare label"
			bareCount++
		}
		if baredesc != wantBare {
			t.Fatalf("bare description %s", word)
		}
		counts[cat]++
		total++
		samples[word] = []any{cat, bare}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if total != 494 || bareCount != 455 || !reflect.DeepEqual(counts, map[string]int{"U": 330, "C": 63, "T": 23, "R": 78}) {
		t.Fatalf("inventory total=%d bare=%d counts=%v", total, bareCount, counts)
	}
	for _, s := range []struct {
		word, cat string
		bare      bool
	}{
		{"abort", "U", true}, {"by", "U", true}, {"insert", "U", true}, {"text", "U", true}, {"merge", "U", true}, {"virtual", "U", true}, {"between", "C", true}, {"bigint", "C", true}, {"integer", "C", true}, {"values", "C", true}, {"json", "C", true}, {"char", "C", false}, {"day", "U", false}, {"authorization", "T", true}, {"join", "T", true}, {"isnull", "T", false}, {"select", "R", true}, {"check", "R", true}, {"analyze", "R", true}, {"end", "R", true}, {"do", "R", true}, {"grant", "R", false}, {"from", "R", false}, {"where", "R", false}, {"order", "R", false},
		{"notnull", "T", false}, {"default", "R", true},
	} {
		if !reflect.DeepEqual(samples[s.word], []any{s.cat, s.bare}) {
			t.Fatalf("sample %s=%v", s.word, samples[s.word])
		}
	}
	for _, word := range []string{"bool", "int4", "int8", "date", "uuid", "serial", "jsonb", "bytea", "float4", "float8"} {
		if _, ok := samples[word]; ok {
			t.Fatalf("unexpected keyword %s", word)
		}
	}
}
