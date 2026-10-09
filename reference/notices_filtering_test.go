package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReferenceNoticesFiltering(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var notices, peerNotices []referenceDiagnostic
			_, c, record := noticeReferencePool(t, p, backend.pg, &notices)
			_, peer, peerRecord := noticeReferencePool(t, p, backend.pg, &peerNotices)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, record, s) }
			noticesShow(t, p.ctx, c, record, "client_min_messages", "notice")
			for _, tc := range []struct{ input, want string }{{"WARNING", "warning"}, {"debug", "debug2"}, {"info", "info"}, {"DEFAULT", "notice"}} {
				run(referenceExec("SET client_min_messages TO "+tc.input, 0))
				noticesShow(t, p.ctx, c, record, "client_min_messages", tc.want)
				noticesShow(t, p.ctx, peer, peerRecord, "client_min_messages", "notice")
			}
			run(referenceExec("SET client_min_messages TO warning", 0))
			for _, mode := range []string{"exec", "prepared-exec"} {
				before := len(notices)
				err := noticesCommand(t, p.ctx, c, record, &notices, mode, "SET client_min_messages TO bogus")
				referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `invalid value for parameter "client_min_messages": "bogus"`, Hint: "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."})
				noticesShow(t, p.ctx, c, record, "client_min_messages", "warning")
				if len(notices) != before {
					t.Fatal("invalid threshold emitted notice", notices[before:])
				}
			}
			err := noticesCommand(t, p.ctx, c, record, &notices, "exec", "SET client_min_messages TO notice,warning")
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "SET client_min_messages takes only one argument"})
			noticesShow(t, p.ctx, c, record, "client_min_messages", "warning")
			run(referenceExec("CREATE TABLE filtered(id INT)", 0))
			for _, threshold := range []string{"debug5", "debug4", "debug3", "debug2", "debug1", "log", "info", "notice", "warning", "error", "debug"} {
				run(referenceExec("SET client_min_messages TO "+threshold, 0))
				delivered := threshold != "warning" && threshold != "error"
				original, clipped := strings.Repeat("a", 64), strings.Repeat("a", 63)
				want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
				for _, mode := range []string{"exec", "prepared-exec"} {
					before := len(notices)
					if err := noticesCommand(t, p.ctx, c, record, &notices, mode, "SET application_name TO '"+original+"'", threshold); err != nil {
						t.Fatal(err)
					}
					noticesWitness(t, backend.pg, notices[before:], want, delivered, "SET application_name TO '"+original+"'")
					noticesShow(t, p.ctx, c, record, "application_name", clipped)
				}
				before := len(notices)
				run(referenceExec("CREATE TABLE IF NOT EXISTS filtered(other TEXT)", 0))
				want = referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "filtered" already exists, skipping`}
				noticesWitness(t, backend.pg, notices[before:], want, delivered)
				before = len(notices)
				run(referenceImplicitError("CREATE TABLE filtered(other TEXT)", referenceDiagnostic{Code: "42P07", Message: `relation "filtered" already exists`}))
				noticesWitness(t, backend.pg, notices[before:], referenceDiagnostic{Code: "42P07", Message: `relation "filtered" already exists`}, false)
			}
			run(referenceExec("SET client_min_messages TO DEFAULT", 0))
			noticesShow(t, p.ctx, c, record, "client_min_messages", "notice")
			run(referenceExec("SET application_name TO '"+strings.Repeat("a", 62)+"é'", 0))
			noticesShow(t, p.ctx, c, record, "application_name", strings.Repeat("a", 62))
			run(referenceExec("SET application_name TO 'é'", 0))
			noticesShow(t, p.ctx, c, record, "application_name", `\xc3\xa9`)
			if len(peerNotices) != 0 {
				t.Fatal("notice leaked to peer", peerNotices)
			}
		})
	}
}

func TestReferenceNoticesApplicationName(t *testing.T) {
	for _, mode := range []string{"exec", "query", "prepared-exec", "prepared-query", "prepared-exec-noncontext", "prepared-query-noncontext"} {
		t.Run(mode, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					var notices []referenceDiagnostic
					_, c, record := noticeReferencePool(t, p, backend.pg, &notices)
					for _, tc := range []struct{ original, clipped string }{{strings.Repeat("a", 64), strings.Repeat("a", 63)}, {strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)}} {
						before := len(notices)
						if err := noticesCommand(t, p.ctx, c, record, &notices, mode, "SET application_name TO '"+tc.original+"'"); err != nil {
							t.Fatal(err)
						}
						want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + tc.original + `" will be truncated to "` + tc.clipped + `"`}
						if len(notices) != before+1 || notices[before] != want {
							t.Fatal(notices)
						}
						noticesShow(t, p.ctx, c, record, "application_name", tc.clipped)
						if backend.pg {
							referenceSessionCancel(t, p.ctx, c, false)
						} else {
							ctx, cancel := context.WithCancel(p.ctx)
							cancel()
							result, err := c.ExecContext(ctx, "SET application_name TO changed")
							if result != nil || !errors.Is(err, context.Canceled) {
								t.Fatal(result, err)
							}
						}
						noticesShow(t, p.ctx, c, record, "application_name", tc.clipped)
						if len(notices) != before+1 || notices[before] != want {
							t.Fatal(notices)
						}
					}
				})
			}
		})
	}
}
