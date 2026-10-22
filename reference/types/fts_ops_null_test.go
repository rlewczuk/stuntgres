package reference_test

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSOpsNullEmpty(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			expression, typ string
			arity           int
			vector          bool
		}{
			{"$1::tsquery && $2::tsquery", "TSQUERY", 2, false},
			{"$1::tsquery || $2::tsquery", "TSQUERY", 2, false},
			{"!!$1::tsquery", "TSQUERY", 1, false},
			{"$1::tsquery <-> $2::tsquery", "TSQUERY", 2, false},
			{"$1::tsquery @> $2::tsquery", "BOOL", 2, false},
			{"$1::tsquery <@ $2::tsquery", "BOOL", 2, false},
			{"tsquery_phrase($1::tsquery,$2::tsquery)", "TSQUERY", 2, false},
			{"tsquery_phrase($1::tsquery,$2::tsquery,$3::int4)", "TSQUERY", 3, false},
			{"$1::tsvector || $2::tsvector", "TSVECTOR", 2, true},
			{"tsquery_and($1::tsquery,$2::tsquery)", "TSQUERY", 2, false},
			{"tsquery_or($1::tsquery,$2::tsquery)", "TSQUERY", 2, false},
			{"tsquery_not($1::tsquery)", "TSQUERY", 1, false},
			{"tsq_mcontains($1::tsquery,$2::tsquery)", "BOOL", 2, false},
			{"tsq_mcontained($1::tsquery,$2::tsquery)", "BOOL", 2, false},
			{"tsvector_concat($1::tsvector,$2::tsvector)", "TSVECTOR", 2, true},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				query := "SELECT " + tc.expression + " AS result"
				stmt, err := b.DB.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
				for _, other := range []string{"a", ""} {
					args := []any{other}
					if tc.arity >= 2 {
						args = append(args, other)
					}
					if tc.arity == 3 {
						args = append(args, int64(1))
					}
					for nullAt := range tc.arity {
						values := append([]any(nil), args...)
						values[nullAt] = nil
						s := fixture.Select(query, []string{"result"}, []string{tc.typ}, [][]any{{nil}}, values...)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						literal := tc.expression
						for i := tc.arity; i >= 1; i-- {
							replacement := "NULL"
							if i-1 != nullAt {
								replacement = ftsTypesQuote(other)
								if i == 3 {
									replacement = "1"
								}
							}
							literal = strings.ReplaceAll(literal, "$"+fixture.Position(i), replacement)
						}
						ftsOpsValue(t, p, b, literal, tc.typ, nil)
						unknown := strings.ReplaceAll(literal, "NULL::tsquery", "NULL")
						unknown = strings.ReplaceAll(unknown, "NULL::tsvector", "NULL")
						unknown = strings.ReplaceAll(unknown, "NULL::int4", "NULL")
						ftsOpsValue(t, p, b, unknown, tc.typ, nil)
					}
				}
				values := make([]any, tc.arity)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"result"}, []string{tc.typ}, [][]any{{nil}}, values...))
				if tc.arity >= 2 {
					for _, pair := range [][2]string{{"", "a"}, {"a", ""}} {
						args := []any{pair[0], pair[1]}
						if tc.arity == 3 {
							args = append(args, int64(1))
						}
						var want any = "'a'"
						if tc.typ == "BOOL" {
							contains := strings.Contains(tc.expression, "@>") || strings.Contains(tc.expression, "tsq_mcontains(")
							if contains {
								want = pair[1] == ""
							} else {
								want = pair[0] == ""
							}
						}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"result"}, []string{tc.typ}, [][]any{{want}}, args...))
					}
				}
				// Repeated held alternation catches stale result/NULL state.
				for _, input := range []any{"a", "", nil, "a", nil, ""} {
					args := []any{input}
					if tc.arity >= 2 {
						args = append(args, input)
					}
					if tc.arity == 3 {
						args = append(args, int64(1))
					}
					var want any
					if input != nil {
						if tc.typ == "BOOL" {
							want = true
						} else if input == "" {
							want = ""
						} else if tc.vector {
							want = "'a'"
						} else {
							switch {
							case tc.arity == 1:
								want = "!'a'"
							case strings.Contains(tc.expression, "&&") || strings.Contains(tc.expression, "tsquery_and("):
								want = "'a' & 'a'"
							case strings.Contains(tc.expression, "||") || strings.Contains(tc.expression, "tsquery_or("):
								want = "'a' | 'a'"
							default:
								want = "'a' <-> 'a'"
							}
						}
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"result"}, []string{tc.typ}, [][]any{{want}}, args...))
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(query+" WHERE FALSE", []string{"result"}, []string{tc.typ}, nil, values...))
			})
		}
		var notices []fixture.Diagnostic
		_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		assert := func(want ...fixture.Diagnostic) {
			t.Helper()
			if len(notices) != len(want) || (len(want) > 0 && !reflect.DeepEqual(notices, want)) {
				t.Fatalf("notices=%#v want=%#v", notices, want)
			}
			notices = nil
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_ops_empty(q tsquery,v tsvector)", 0))
		assert()
		run(fixture.Exec("INSERT INTO fts_ops_empty VALUES($1,$2)", 1, "", ""))
		assert(ftsTypesEmptyNotice("", "", "unnamed portal parameter $1 = ''"))
		for _, expr := range []string{"q && q", "q || q", "!!q", "q <-> q", "tsquery_phrase(q,q)", "tsquery_phrase(q,q,0)", "v || v"} {
			typ := "TSQUERY"
			if expr == "v || v" {
				typ = "TSVECTOR"
			}
			run(fixture.Select("SELECT "+expr+" AS result FROM fts_ops_empty", []string{"result"}, []string{typ}, [][]any{{""}}))
			assert()
		}
		for _, expr := range []string{"q @> q", "q <@ q"} {
			run(fixture.Select("SELECT "+expr+" AS result FROM fts_ops_empty", []string{"result"}, []string{"BOOL"}, [][]any{{true}}))
			assert()
		}
		query := "SELECT ''::tsquery && 'a'::tsquery AS result"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		assert(ftsTypesEmptyNotice("", "8", ""))
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
		for range 3 {
			fixture.RunStep(t, p.Ctx, held, recorder, fixture.Select(query, []string{"result"}, []string{"TSQUERY"}, [][]any{{"'a'"}}))
			assert()
		}
		bind := "SELECT $1::tsquery && 'a'::tsquery AS result"
		bound, err := conn.PrepareContext(p.Ctx, bind)
		if err != nil {
			t.Fatal(err)
		}
		defer bound.Close()
		assert()
		for range 3 {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: bound, SQL: bind}, recorder, fixture.Select(bind, []string{"result"}, []string{"TSQUERY"}, [][]any{{"'a'"}}, ""))
			assert(ftsTypesEmptyNotice("", "", "unnamed portal parameter $1 = ''"))
		}
	})
}
