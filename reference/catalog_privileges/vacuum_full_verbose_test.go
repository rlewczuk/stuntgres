package reference_test

import (
	"reflect"
	"regexp"
	"stuntgres/reference/fixture"
)

func fullVerbose(f *fullVacuumCase) {
	f.t.Helper()
	f.run(fixture.Exec("CREATE TABLE full_verbose(id INT)", 0))
	f.run(fixture.Exec("INSERT INTO full_verbose VALUES(1),(2)", 2))
	f.run(fixture.Exec("DELETE FROM full_verbose WHERE id=1", 1))
	before := f.locator("full_verbose")
	var notices []fixture.Diagnostic
	_, worker, rec := fixture.NoticeReferencePool(f.t, f.p, f.b.PG, &notices)
	step := fixture.Exec("VACUUM (FULL,VERBOSE) full_verbose", 0)
	if f.prepared {
		fixture.RunStep(f.t, f.p.Ctx, vacuumHeld(f.t, f.p, worker, step.Query), rec, step)
	} else {
		fixture.RunStep(f.t, f.p.Ctx, worker, rec, step)
	}
	f.rewritten("full_verbose", before)
	if f.b.PG {
		// Released cluster.c copy_table_data emits exactly these two INFO
		// records. Only physical page count and measured CPU/time vary.
		if len(notices) != 2 {
			f.t.Fatalf("FULL VERBOSE notices %#v", notices)
		}
		first := fixture.Diagnostic{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: `vacuuming "public.full_verbose"`}
		if notices[0] != first {
			f.t.Fatalf("FULL entry diagnostic %#v want %#v", notices[0], first)
		}
		message := regexp.MustCompile(`^"public\.full_verbose": found 1 removable, 1 nonremovable row versions in [1-9][0-9]* pages$`)
		detail := regexp.MustCompile(`^0 dead row versions cannot be removed yet\.\nCPU: user: [0-9]+\.[0-9]{2} s, system: [0-9]+\.[0-9]{2} s, elapsed: [0-9]+\.[0-9]{2} s\.$`)
		if !message.MatchString(notices[1].Message) || !detail.MatchString(notices[1].Detail) {
			f.t.Fatalf("FULL physical summary %#v", notices[1])
		}
		summary := notices[1]
		summary.Message = "physical summary"
		summary.Detail = ""
		want := fixture.Diagnostic{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: "physical summary"}
		if summary != want {
			f.t.Fatalf("FULL summary portable fields %#v", summary)
		}
	} else {
		want := []fixture.Diagnostic{{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: `rewriting "public"."full_verbose" (native logical storage): 1 live rows, 0 retained dead versions, 1 dead versions removed, 1 versions frozen, 0 indexes rebuilt`}}
		if !reflect.DeepEqual(notices, want) {
			f.t.Fatalf("native logical FULL report %#v want %#v", notices, want)
		}
	}
	vacuumRows(f.t, f.p, f.b, f.c, "full_verbose", 2)
}
