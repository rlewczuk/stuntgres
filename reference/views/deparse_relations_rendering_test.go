package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeparseRelationsViewPrettyWrap(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.DeparseRelationsSeed(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE VIEW wrapped AS SELECT id AS "a very long été identifier", 'literal with spaces and été that must not split'::text AS literal, CASE WHEN visible THEN id-(id-1) ELSE 0 END AS answer FROM t WHERE visible`, 0))
		calls := []string{"pg_get_viewdef('wrapped'::text)", "pg_get_viewdef('wrapped'::text,false)", "pg_get_viewdef('wrapped'::text,true)", "pg_get_viewdef('wrapped'::regclass,true)"}
		for _, n := range []int64{-2147483648, -1, 0, 1, 20, 80, 2147483647} {
			calls = append(calls, fmt.Sprintf("pg_get_viewdef('wrapped'::regclass,%d)", n))
		}
		definitions := make([]string, len(calls))
		want := [][]any{{int64(1), "literal with spaces and été that must not split", int64(1)}, {int64(3), "literal with spaces and été that must not split", int64(1)}, {int64(3), "literal with spaces and été that must not split", int64(1)}}
		for i, call := range calls {
			d := fixture.DeparseRelationsRead(t, p, b, c, "SELECT "+call+" AS definition")
			definitions[i] = d
			if !strings.HasSuffix(d, ";") || !strings.Contains(d, `"a very long été identifier"`) || !strings.Contains(d, "literal with spaces and été that must not split") {
				t.Fatalf("split token/label or lost terminator %q", d)
			}
			name := fmt.Sprintf("wrap_copy_%d", i)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW "+name+" AS "+d, 0))
			s := fixture.Select("SELECT * FROM "+name, []string{"a very long été identifier", "literal", "answer"}, []string{"INT4", "TEXT", "INT4"}, want)
			s.Bag = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
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
