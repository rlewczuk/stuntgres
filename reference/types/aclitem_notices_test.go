package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func aclitemWarning(position, context string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0L000", Message: "defaulting grantor to user ID 10", Position: position, Context: context}
}

func TestReferenceACLItemNotices(t *testing.T) {
	aclitemScenario(t, []string{"acl_notice_actor"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		// The pinned container names bootstrap ID 10 stuntgres; the native
		// fixture names that same bootstrap identity postgres.
		defaultGrantor := "postgres"
		if b.PG {
			defaultGrantor = "stuntgres"
		}
		var notices []fixture.Diagnostic
		_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
		assert := func(t *testing.T, want ...fixture.Diagnostic) {
			t.Helper()
			if len(notices) != len(want) || (len(want) > 0 && !reflect.DeepEqual(notices, want)) {
				t.Fatalf("notices=%#v want %#v", notices, want)
			}
			notices = nil
		}
		run(fixture.Exec("SET client_min_messages = warning", 0))
		for _, tc := range []struct {
			name, query, input string
			array, bind        bool
			count              int
		}{
			{"literal", "SELECT 'postgres=r'::aclitem AS a", "", false, false, 1},
			{"bind", "SELECT $1::aclitem AS a", "postgres=r", false, true, 1},
			{"array_literal", "SELECT '{postgres=r,=w}'::aclitem[] AS a", "", true, false, 2},
			{"array_bind", "SELECT $1::aclitem[] AS a", "{postgres=r,=w}", true, true, 2},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.name+"_"+fixture.Position(1+boolInt(prepared)), func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
					notices = nil
					context := ""
					if tc.bind {
						context = "unnamed portal parameter $1 = '...'"
					}
					position := "8"
					if tc.bind {
						position = ""
					}
					want := make([]fixture.Diagnostic, tc.count)
					for i := range want {
						want[i] = aclitemWarning(position, context)
					}
					types := []string{"ACLITEM"}
					out := "postgres=r/" + defaultGrantor
					if tc.array {
						types = []string{"_ACLITEM"}
						out = "{postgres=r/" + defaultGrantor + ",=w/" + defaultGrantor + "}"
					}
					var args []any
					if tc.bind {
						args = []any{tc.input}
					}
					s := fixture.Select(tc.query, []string{"a"}, types, [][]any{{out}}, args...)
					if !prepared {
						run(s)
						assert(t, want...)
						return
					}
					stmt, err := c.PrepareContext(p.Ctx, tc.query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					if tc.bind {
						assert(t)
					} else {
						assert(t, want...)
					}
					for range 2 {
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}, recorder, s)
						if tc.bind {
							assert(t, want...)
						} else {
							assert(t)
						}
					}
				})
			}
		}
		t.Run("trailing_whitespace", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
			notices = nil
			run(aclitemSelect("SELECT 'postgres=r \t\n'::aclitem AS a", [][]any{{"postgres=r/" + defaultGrantor}}))
			assert(t, aclitemWarning("8", ""))
		})
		run(fixture.Exec("CREATE TABLE public.acl_notice_atomic (id INT,a aclitem)", 0))
		run(fixture.Exec("INSERT INTO public.acl_notice_atomic VALUES (7,'=r/postgres')", 1))
		for _, input := range []string{"postgres=r1/postgres", "postgres=r=/postgres", "postgres= r/postgres", "postgres=r /postgres", "postgres=r!"} {
			for _, prepared := range []bool{false, true} {
				for _, bind := range []bool{false, true} {
					t.Run("warning_atomic_"+input+"_"+fixture.Position(1+boolInt(prepared)*2+boolInt(bind)), func(t *testing.T) {
						notices = nil
						prefix := "INSERT INTO public.acl_notice_atomic VALUES (1,'=r/postgres'),(2,"
						q := prefix + fixture.ArrayReferenceQuote(input) + "::aclitem)"
						d := aclitemDiagnostic("22P02", "extra garbage at the end of the ACL specification")
						var args []any
						context := ""
						if bind {
							q = prefix + "$1::aclitem)"
							args = []any{input}
							context = "unnamed portal parameter $1 = '...'"
							d.Context = context
						} else {
							d.Position = fixture.Position(len(prefix) + 1)
						}
						s := fixture.ErrorDiagnostic(q, d, args...)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared && !bind {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, recorder, s)
						assert(t, aclitemWarning(d.Position, context))
						fixture.RunStep(t, p.Ctx, c, recorder, fixture.Select("SELECT id,a FROM public.acl_notice_atomic ORDER BY id", []string{"id", "a"}, []string{"INT4", "ACLITEM"}, [][]any{{int64(7), "=r/postgres"}}))
					})
				}
			}
		}
		t.Run("explicit_grantor", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
			notices = nil
			run(aclitemSelect("SELECT 'postgres=r/postgres'::aclitem AS a", [][]any{{"postgres=r/postgres"}}))
			assert(t)
		})
		t.Run("actor_defaults_bootstrap", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
			notices = nil
			run(fixture.Exec("SET ROLE acl_notice_actor", 0))
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			run(aclitemSelect("SELECT '=r'::aclitem AS a", [][]any{{"=r/" + defaultGrantor}}))
			assert(t, aclitemWarning("8", ""))
			run(fixture.Exec("RESET ROLE", 0))
		})
		t.Run("warning_before_error", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
			notices = nil
			d := aclitemDiagnostic("22P02", "extra garbage at the end of the ACL specification")
			d.Position = "8"
			s := fixture.ErrorDiagnostic("SELECT 'postgres=r!'::aclitem AS a", d)
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			assert(t, aclitemWarning("8", ""))
		})
		t.Run("client_filter_and_drain", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
			notices = nil
			run(fixture.Exec("SET client_min_messages = error", 0))
			run(aclitemSelect("SELECT 'postgres=r'::aclitem AS a", [][]any{{"postgres=r/" + defaultGrantor}}))
			assert(t)
			run(fixture.Exec("SET client_min_messages = warning", 0))
			run(aclitemSelect("SELECT 'postgres=r/postgres'::aclitem AS a", [][]any{{"postgres=r/postgres"}}))
			assert(t)
		})
		t.Run("peer_notice_ownership", func(t *testing.T) {
			var peerNotices []fixture.Diagnostic
			_, peer, peerRecorder := fixture.NoticeReferencePool(t, p, b.PG, &peerNotices)
			notices = nil
			fixture.RunStep(t, p.Ctx, peer, peerRecorder, aclitemSelect("SELECT '=w'::aclitem AS a", [][]any{{"=w/" + defaultGrantor}}))
			assert(t)
			if !reflect.DeepEqual(peerNotices, []fixture.Diagnostic{aclitemWarning("8", "")}) {
				t.Fatalf("peer notices=%#v", peerNotices)
			}
		})
	})
}
