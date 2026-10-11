package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNoticesFiltering(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices, peerNotices []fixture.Diagnostic
			_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			_, peer, peerRecord := fixture.NoticeReferencePool(t, p, backend.PG, &peerNotices)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, record, s) }
			noticesShow(t, p.Ctx, c, record, "client_min_messages", "notice")
			for _, tc := range []struct{ input, want string }{{"WARNING", "warning"}, {"debug", "debug2"}, {"info", "info"}, {"DEFAULT", "notice"}} {
				run(fixture.Exec("SET client_min_messages TO "+tc.input, 0))
				noticesShow(t, p.Ctx, c, record, "client_min_messages", tc.want)
				noticesShow(t, p.Ctx, peer, peerRecord, "client_min_messages", "notice")
			}
			run(fixture.Exec("SET client_min_messages TO warning", 0))
			for _, mode := range []string{"exec", "prepared-exec"} {
				before := len(notices)
				err := noticesCommand(t, p.Ctx, c, record, &notices, mode, "SET client_min_messages TO bogus")
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `invalid value for parameter "client_min_messages": "bogus"`, Hint: "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."})
				noticesShow(t, p.Ctx, c, record, "client_min_messages", "warning")
				if len(notices) != before {
					t.Fatal("invalid threshold emitted notice", notices[before:])
				}
			}
			err := noticesCommand(t, p.Ctx, c, record, &notices, "exec", "SET client_min_messages TO notice,warning")
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "SET client_min_messages takes only one argument"})
			noticesShow(t, p.Ctx, c, record, "client_min_messages", "warning")
			run(fixture.Exec("CREATE TABLE filtered(id INT)", 0))
			for _, threshold := range []string{"debug5", "debug4", "debug3", "debug2", "debug1", "log", "info", "notice", "warning", "error", "debug"} {
				run(fixture.Exec("SET client_min_messages TO "+threshold, 0))
				delivered := threshold != "warning" && threshold != "error"
				original, clipped := strings.Repeat("a", 64), strings.Repeat("a", 63)
				want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
				for _, mode := range []string{"exec", "prepared-exec"} {
					before := len(notices)
					if err := noticesCommand(t, p.Ctx, c, record, &notices, mode, "SET application_name TO '"+original+"'", threshold); err != nil {
						t.Fatal(err)
					}
					noticesWitness(t, backend.PG, notices[before:], want, delivered, "SET application_name TO '"+original+"'")
					noticesShow(t, p.Ctx, c, record, "application_name", clipped)
				}
				before := len(notices)
				run(fixture.Exec("CREATE TABLE IF NOT EXISTS filtered(other TEXT)", 0))
				want = fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "filtered" already exists, skipping`}
				noticesWitness(t, backend.PG, notices[before:], want, delivered)
				before = len(notices)
				run(fixture.ImplicitError("CREATE TABLE filtered(other TEXT)", fixture.Diagnostic{Code: "42P07", Message: `relation "filtered" already exists`}))
				noticesWitness(t, backend.PG, notices[before:], fixture.Diagnostic{Code: "42P07", Message: `relation "filtered" already exists`}, false)
			}
			run(fixture.Exec("SET client_min_messages TO DEFAULT", 0))
			noticesShow(t, p.Ctx, c, record, "client_min_messages", "notice")
			run(fixture.Exec("SET application_name TO '"+strings.Repeat("a", 62)+"é'", 0))
			noticesShow(t, p.Ctx, c, record, "application_name", strings.Repeat("a", 62))
			run(fixture.Exec("SET application_name TO 'é'", 0))
			noticesShow(t, p.Ctx, c, record, "application_name", `\xc3\xa9`)
			if len(peerNotices) != 0 {
				t.Fatal("notice leaked to peer", peerNotices)
			}
		})
	}
}

func TestReferenceNoticesApplicationName(t *testing.T) {
	for _, mode := range []string{"exec", "query", "prepared-exec", "prepared-query", "prepared-exec-noncontext", "prepared-query-noncontext"} {
		t.Run(mode, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					for _, tc := range []struct{ original, clipped string }{{strings.Repeat("a", 64), strings.Repeat("a", 63)}, {strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)}} {
						before := len(notices)
						if err := noticesCommand(t, p.Ctx, c, record, &notices, mode, "SET application_name TO '"+tc.original+"'"); err != nil {
							t.Fatal(err)
						}
						want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + tc.original + `" will be truncated to "` + tc.clipped + `"`}
						if len(notices) != before+1 || notices[before] != want {
							t.Fatal(notices)
						}
						noticesShow(t, p.Ctx, c, record, "application_name", tc.clipped)
						if backend.PG {
							fixture.SessionCancel(t, p.Ctx, c, false)
						} else {
							ctx, cancel := context.WithCancel(p.Ctx)
							cancel()
							result, err := c.ExecContext(ctx, "SET application_name TO changed")
							if result != nil || !errors.Is(err, context.Canceled) {
								t.Fatal(result, err)
							}
						}
						noticesShow(t, p.Ctx, c, record, "application_name", tc.clipped)
						if len(notices) != before+1 || notices[before] != want {
							t.Fatal(notices)
						}
					}
				})
			}
		})
	}
}
