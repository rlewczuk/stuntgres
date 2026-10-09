package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceDeparseRelationsViewPrettyWrap(t *testing.T) {
	deparseRelationsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		deparseRelationsSeed(t, p, b, c)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`CREATE VIEW wrapped AS SELECT id AS "a very long été identifier", 'literal with spaces and été that must not split'::text AS literal, CASE WHEN visible THEN id-(id-1) ELSE 0 END AS answer FROM t WHERE visible`, 0))
		calls := []string{"pg_get_viewdef('wrapped'::text)", "pg_get_viewdef('wrapped'::text,false)", "pg_get_viewdef('wrapped'::text,true)", "pg_get_viewdef('wrapped'::regclass,true)"}
		for _, n := range []int64{-2147483648, -1, 0, 1, 20, 80, 2147483647} {
			calls = append(calls, fmt.Sprintf("pg_get_viewdef('wrapped'::regclass,%d)", n))
		}
		definitions := make([]string, len(calls))
		want := [][]any{{int64(1), "literal with spaces and été that must not split", int64(1)}, {int64(3), "literal with spaces and été that must not split", int64(1)}, {int64(3), "literal with spaces and été that must not split", int64(1)}}
		for i, call := range calls {
			d := deparseRelationsRead(t, p, b, c, "SELECT "+call+" AS definition")
			definitions[i] = d
			if !strings.HasSuffix(d, ";") || !strings.Contains(d, `"a very long été identifier"`) || !strings.Contains(d, "literal with spaces and été that must not split") {
				t.Fatalf("split token/label or lost terminator %q", d)
			}
			name := fmt.Sprintf("wrap_copy_%d", i)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE VIEW "+name+" AS "+d, 0))
			s := referenceSelect("SELECT * FROM "+name, []string{"a very long été identifier", "literal", "answer"}, []string{"INT4", "TEXT", "INT4"}, want)
			s.bag = true
			referenceRunStep(t, p.ctx, c, b.recorder, s)
			referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
		}
		if definitions[0] != definitions[1] {
			t.Fatal("omitted pretty differs from false")
		}
		if definitions[2] != definitions[3] {
			t.Fatal("text/oid pretty differ")
		}
		if definitions[2] != definitions[6] {
			t.Fatal("pretty=true differs from wrap=0")
		}
		// Assert the observed meaningful target placement, not incidental CASE
		// indentation or trailing spaces. The shared replay above independently
		// checks subtraction precedence, output labels/types and duplicate rows.
		for i, definition := range definitions {
			first := strings.Index(definition, `"a very long été identifier"`)
			second := strings.Index(definition, "'literal with spaces and été that must not split'")
			if first < 0 || second <= first {
				t.Fatalf("missing or reordered complete targets in %s: %q", calls[i], definition)
			}
			first += len(`"a very long été identifier"`)
			sameLine := !strings.Contains(definition[first:second], "\n")
			wantSameLine := i == 4 || i == 5 || i == 10
			if sameLine != wantSameLine {
				t.Errorf("target placement %s sameLine=%v want %v: %q", calls[i], sameLine, wantSameLine, definition)
			}
		}
	})
}
