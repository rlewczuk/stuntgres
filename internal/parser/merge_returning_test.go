package parser

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMergeReturningGrammar(t *testing.T) {
	q := "/*é*/ MERGE INTO t AS target USING source s ON target.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v RETURNING WITH(OLD AS o,NEW AS n) merge_action() AS action,o.v,n.v"
	stmts, err := Parse(q)
	if err != nil || len(stmts) != 1 {
		t.Fatal(stmts, err)
	}
	m := stmts[0].(*Merge)
	if m.Returning == nil || m.Alias.Text != "target" || len(m.Returning.Targets) != 3 || len(m.Returning.Aliases) != 2 {
		t.Fatal(m)
	}
	r := m.Returning
	if r.Aliases[0].New || !r.Aliases[1].New || r.Aliases[0].Alias.Text != "o" || r.Aliases[1].Alias.Text != "n" || r.Labels[0].Text != "action" {
		t.Fatal(r)
	}
	action, ok := r.Targets[0].(MergeActionExpr)
	if !ok {
		t.Fatalf("special syntax lost: %T", r.Targets[0])
	}
	if action.Pos.Character != utf8.RuneCountInString(q[:strings.Index(q, "merge_action")])+1 || r.Pos.Character != utf8.RuneCountInString(q[:strings.Index(q, "RETURNING")])+1 {
		t.Fatal("character positions", action.Pos, r.Pos)
	}
	for _, suffix := range []string{" RETURNING", " RETURNING WITH() id", " RETURNING merge_action(1)", " RETURNING id garbage garbage", " RETURNING id; garbage"} {
		stmts, err = Parse("MERGE INTO t USING source s ON TRUE WHEN MATCHED THEN DELETE" + suffix)
		if err == nil || len(stmts) != 0 {
			t.Fatal("invalid complete suffix accepted", suffix, stmts, err)
		}
	}
	// Keyword syntax remains distinct from quoted/qualified generic calls.
	for _, q := range []string{`SELECT "merge_action"()`, `SELECT public.merge_action()`} {
		stmts, err = Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := stmts[0].(*Select).Targets[0].(FunctionExpr); !ok {
			t.Fatal("generic spelling converted to special syntax", q)
		}
	}
}
func TestMergeReturningQuerySources(t *testing.T) {
	q := "MERGE INTO t USING source s ON EXISTS(SELECT 1 FROM on_source) WHEN MATCHED AND EXISTS(SELECT 1 FROM conditions) THEN UPDATE SET v=(SELECT v FROM payload) RETURNING merge_action(),(SELECT v FROM output_only),(SELECT v FROM output_only)"
	stmts, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var refs []*TableReference
	err = VisitQuerySources(stmts[0], nil, func(table *TableReference, _ *Select) error {
		names = append(names, table.Name.Parts[0].Text)
		refs = append(refs, table)
		return nil
	})
	if err != nil || !reflect.DeepEqual(names, []string{"source", "on_source", "output_only", "output_only", "conditions", "payload"}) {
		t.Fatal("missing output occurrence", names, err)
	}
	if refs[2] == refs[3] {
		t.Fatal("output relation occurrences deduplicated")
	}
}
