package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func deparseRelationsReviewScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func TestReferenceDeparseRelationsTopLevelValues(t *testing.T) {
	deparseRelationsReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE VIEW bare_values AS VALUES(1,'a'::text),(3,NULL::text),(3,NULL::text)", 0))
		for _, renamed := range []bool{false, true} {
			names := []string{"column1", "column2"}
			if renamed {
				run(fixture.Exec("ALTER VIEW bare_values RENAME COLUMN column1 TO key", 0))
				run(fixture.Exec("ALTER VIEW bare_values RENAME COLUMN column2 TO label", 0))
				names = []string{"key", "label"}
			}
			var defaultDefinition string
			for i, call := range []string{"pg_get_viewdef('bare_values'::text)", "pg_get_viewdef('bare_values'::text,false)", "pg_get_viewdef('bare_values'::text,true)", "pg_get_viewdef('bare_values'::regclass,1)", "pg_get_viewdef('bare_values'::regclass,80)", "pg_get_viewdef('bare_values'::regclass,-1)"} {
				d := fixture.DeparseRelationsRead(t, p, b, c, "SELECT "+call+" AS definition")
				// PG18 keeps plain top-level VALUES; renamed descriptor labels
				// require a SELECT projection. Do not force the plain form into
				// SELECT merely from the reviewer's unobserved assumption.
				prefix := "VALUES "
				if renamed {
					prefix = "SELECT "
				}
				if !strings.HasPrefix(strings.TrimSpace(d), prefix) {
					t.Errorf("top-level VALUES %s renamed=%v got %q; expected %s form", call, renamed, d, prefix)
				}
				if i == 0 {
					defaultDefinition = d
				}
				if (i == 1 || !renamed) && d != defaultDefinition {
					t.Errorf("top-level VALUES default/form equality %s renamed=%v got %q want %q", call, renamed, d, defaultDefinition)
				}
				if renamed {
					first, second := strings.Index(d, "key"), strings.Index(d, "label")
					if first < 0 || second <= first {
						t.Fatalf("current VALUES output labels missing: %q", d)
					}
					sameLine := !strings.Contains(d[first:second], "\n")
					wantSameLine := i == 4 || i == 5
					if sameLine != wantSameLine {
						t.Errorf("renamed VALUES target placement %s sameLine=%v want%v: %q", call, sameLine, wantSameLine, d)
					}
				}
				name := fmt.Sprintf("values_copy_%v_%d", renamed, i)
				run(fixture.Exec("CREATE VIEW "+name+" AS "+d, 0))
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT * FROM "+name, names, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), nil}, {int64(3), nil}})
					s.Bag = true
					s.Prepared = prepared
					run(s)
				}
			}
		}
	})
}

func TestReferenceDeparseRelationsPositiveByteWrap(t *testing.T) {
	deparseRelationsReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.DeparseRelationsSeed(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE VIEW byte_wrap AS SELECT id AS "éé",txt AS t FROM t`, 0))
		// The independently specified complete line has30 bytes but28 runes:
		// 8-byte prefix +12-byte first target +2-byte separator +8-byte second target.
		for _, tc := range []struct {
			width    int
			sameLine bool
		}{{29, false}, {30, true}} {
			q := fmt.Sprintf("SELECT pg_get_viewdef('byte_wrap'::regclass,%d) AS definition", tc.width)
			d := fixture.DeparseRelationsRead(t, p, b, c, q)
			first := strings.Index(d, `"éé"`)
			second := strings.Index(d, "txt AS t")
			if first < 0 || second <= first {
				t.Fatalf("byte-boundary targets missing: %q", d)
			}
			sameLine := !strings.Contains(d[first:second], "\n")
			if sameLine != tc.sameLine {
				t.Errorf("width%d target sameLine=%v want%v: %q", tc.width, sameLine, tc.sameLine, d)
			}
			name := fmt.Sprintf("byte_copy_%d", tc.width)
			run(fixture.Exec("CREATE VIEW "+name+" AS "+d, 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT * FROM "+name, []string{"éé", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), nil}, {int64(3), nil}})
				s.Bag = true
				s.Prepared = prepared
				run(s)
			}
		}
	})
}

func TestReferenceDeparseRelationsOperatorPretty(t *testing.T) {
	deparseRelationsReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.DeparseRelationsSeed(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE VIEW operator_pretty AS SELECT id-(id-1) AS answer,id=1 OR visible AS chosen FROM t WHERE id=1 OR visible AND txt IS NULL", 0))
		for _, tc := range []struct {
			pretty bool
			want   string
		}{
			{false, " SELECT (id - (id - 1)) AS answer,\n    ((id = 1) OR visible) AS chosen\n   FROM t\n  WHERE ((id = 1) OR (visible AND (txt IS NULL)));"},
			{true, " SELECT id - (id - 1) AS answer,\n    id = 1 OR visible AS chosen\n   FROM t\n  WHERE id = 1 OR visible AND txt IS NULL;"},
		} {
			q := fmt.Sprintf("SELECT pg_get_viewdef('operator_pretty'::text,%v) AS definition", tc.pretty)
			// A small bounded operator fixture pins necessary precedence and a real
			// compact/pretty difference, unlike incidental complex CASE indentation.
			d := fixture.DeparseRelationsRead(t, p, b, c, q)
			name := fmt.Sprintf("operator_copy_%v", tc.pretty)
			if d != tc.want {
				t.Errorf("bounded operator pretty=%v got %q want %q", tc.pretty, d, tc.want)
			}
			run(fixture.Exec("CREATE VIEW "+name+" AS "+d, 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT * FROM "+name, []string{"answer", "chosen"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(1), true}, {int64(1), true}})
				s.Bag = true
				s.Prepared = prepared
				run(s)
			}
		}
	})
}
