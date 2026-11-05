package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const xml2SetsDocument = "<r><a>A<b>B</b>C</a><a>D&amp;E</a></r>"
const xml2SetsNodes = "<a>A<b>B</b>C</a><a>D&amp;E</a>"
const xml2SetsItems = "<i><a>A<b>B</b>C</a></i><i><a>D&amp;E</a></i>"

type xml2SetCall struct {
	name, signature, expression, healthy string
	args []any
}

func xml2SetCalls() []xml2SetCall {
	return []xml2SetCall{
		{"nodes2", "xpath_nodeset(text,text)", "xpath_nodeset($1::text,$2::text)", xml2SetsNodes, []any{xml2SetsDocument, "/r/a"}},
		{"nodes3", "xpath_nodeset(text,text,text)", "xpath_nodeset($1::text,$2::text,$3::text)", xml2SetsItems, []any{xml2SetsDocument, "/r/a", "i"}},
		{"nodes4", "xpath_nodeset(text,text,text,text)", "xpath_nodeset($1::text,$2::text,$3::text,$4::text)", "<o>" + xml2SetsItems + "</o>", []any{xml2SetsDocument, "/r/a", "o", "i"}},
		{"list2", "xpath_list(text,text)", "xpath_list($1::text,$2::text)", "ABC,D&E", []any{xml2SetsDocument, "/r/a"}},
		{"list3", "xpath_list(text,text,text)", "xpath_list($1::text,$2::text,$3::text)", "ABC|D&E", []any{xml2SetsDocument, "/r/a", "|"}},
	}
}

func xml2SetsCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *[]fixture.Diagnostic)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = recorder
			// Keep these long-lived sequences deterministic. The separate
			// Planning matrix exercises forced generic plans as well.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET plan_cache_mode = force_custom_plan", 0))
			defer func() {
				if len(notices) != 0 {
					t.Errorf("unexpected set notices=%#v", notices)
				}
			}()
			body(t, p, b, c, &notices)
		})
	}
}

func xml2SetValue(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{value}}, args...)
}

func xml2SetsQueryError(q string, d fixture.Diagnostic, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

// SQL-standard wrapper attribution is distinct from the kernel's diagnostic.
// The deterministic plan matrix also asserts eligible generic inlining with
// empty Context, without depending on PostgreSQL's automatic cost transition.
func xml2SetsSQLContext(d fixture.Diagnostic, routine string) fixture.Diagnostic {
	if routine != "" {
		d.Context = "SQL function \"" + routine + "\" statement 1"
	}
	return d
}

func xml2SetsWrapperName(call xml2SetCall) string {
	switch call.name {
	case "nodes2", "nodes3":
		return "xpath_nodeset"
	case "list2":
		return "xpath_list"
	default:
		return ""
	}
}

func xml2SetsNotices(t *testing.T, notices *[]fixture.Diagnostic, want ...fixture.Diagnostic) {
	t.Helper()
	if !reflect.DeepEqual(*notices, want) {
		t.Errorf("notices=%#v want=%#v", *notices, want)
	}
	*notices = nil
}
