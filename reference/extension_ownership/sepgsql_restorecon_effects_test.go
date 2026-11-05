package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSepgsqlSimulationRestoreconUnmatched(t *testing.T) {
	setup := func(run sepgsqlRun) {
		for _, name := range []string{"restorecon_skipped", "restorecon_absent", "restorecon_matched"} {
			run(fixture.Exec("CREATE TABLE "+name+"(v text)", 0))
			run(fixture.Exec("INSERT INTO "+name+" VALUES('kept')", 1))
		}
	}
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		run(fixture.ImplicitError("SECURITY LABEL FOR selinux ON TABLE restorecon_skipped IS '"+sepgsqlS+"'", fixture.Diagnostic{Code: "22023", Message: "security label provider \"selinux\" is not loaded"}))
		run(sepgsqlText("SELECT v FROM restorecon_skipped", "v", "kept"))
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.Policy.NamedSpecs["partial"] = strings.Replace(config.Policy.DefaultSpec, "db_table * "+sepgsqlP, "db_table *.public.restorecon_matched "+sepgsqlP, 1)
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		var notices []fixture.Diagnostic
		_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, false, &notices)
		noticeRun := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, noticeConn, recorder, step) }
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT quote_ident(current_database())").Scan(&database); err != nil {
			t.Fatal(err)
		}
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_skipped IS '"+sepgsqlS+"'", 0))
			run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_absent IS NULL", 0))
			run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_matched IS '"+sepgsqlS+"'", 0))
			notices = nil
			noticeRun(fixture.Exec("BEGIN", 0))
			restoreconCall(noticeRun, "sepgsql_restorecon", "partial", prepared)
			for _, name := range []string{"restorecon_skipped", "restorecon_absent"} {
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "SELinux: no initial label assigned for " + database + ".public." + name + " (type=10), skipping"}
				found := false
				for _, notice := range notices {
					if notice.Message == want.Message {
						found = true
						if notice != want {
							t.Fatalf("complete unmatched WARNING: got %#v want %#v", notice, want)
						}
					}
				}
				if !found {
					t.Fatal("missing genuine unmatched warning", name, notices)
				}
			}
			noticeRun(sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='restorecon_skipped'::regclass AND objsubid=0 AND provider='selinux'", "label", sepgsqlS))
			noticeRun(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='restorecon_absent'::regclass AND objsubid=0 AND provider='selinux'", 0))
			noticeRun(sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='restorecon_matched'::regclass AND objsubid=0 AND provider='selinux'", "label", sepgsqlP))
			count := len(notices)
			noticeRun(fixture.Exec("ROLLBACK", 0))
			if len(notices) != count {
				t.Fatal("catalog rollback altered delivered warnings")
			}
			// Apply again outside a transaction: success preserves skipped
			// association absence and installs a real matched association.
			restoreconCall(noticeRun, "sepgsql_restorecon", "partial", prepared)
			for _, name := range []string{"restorecon_skipped", "restorecon_absent", "restorecon_matched"} {
				run(sepgsqlText("SELECT v FROM "+name, "v", "kept"))
			}
			run(sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='restorecon_skipped'::regclass AND objsubid=0 AND provider='selinux'", "label", sepgsqlS))
			run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='restorecon_absent'::regclass AND objsubid=0 AND provider='selinux'", 0))
			run(sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='restorecon_matched'::regclass AND objsubid=0 AND provider='selinux'", "label", sepgsqlP))
		}
	})
}

func TestReferenceSepgsqlSimulationRestoreconRelabelTo(t *testing.T) {
	var events []stuntgres.SepgsqlAuditEvent
	sepgsqlCase(t, restoreconInactive, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.Policy.NamedSpecs["destination-denied"] = "db_column *.public.restorecon_dropped.* " + sepgsqlP + "\ndb_column * " + sepgsqlS + "\n" + config.Policy.DefaultSpec
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlA, Target: sepgsqlP, Class: "db_column", Deny: []string{"relabelto"}})
		config.AuditAllowed = true
		config.Audit = func(event stuntgres.SepgsqlAuditEvent) { events = append(events, event) }
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		restoreconDroppedSetup(run)
		for _, column := range []string{"keep", "tail", "ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
			run(fixture.Exec("SECURITY LABEL ON COLUMN restorecon_dropped."+column+" IS '"+sepgsqlS+"'", 0))
		}
		run(fixture.Exec("SECURITY LABEL ON TABLE restorecon_dropped IS '"+sepgsqlS+"'", 0))
		for _, prepared := range []bool{false, true} {
			before := sepgsqlRestoreconLabels(t, p, c)
			events = nil
			step := sepgsqlDenied("SELECT sepgsql_restorecon('destination-denied')")
			step.Prepared = prepared
			run(step)
			restoreconUnchanged(t, p, c, before)
			found, oldAllowed, stagedEarlier := false, false, false
			for _, event := range events {
				if event.Class == "db_database" && reflect.DeepEqual(event.Required, []string{"relabelto"}) && len(event.Denied) == 0 {
					stagedEarlier = true
				}
				if event.Class == "db_column" && event.Target == sepgsqlS && strings.Contains(event.Name, "restorecon_dropped.") && len(event.Denied) == 0 && reflect.DeepEqual(event.Required, []string{"setattr", "relabelfrom"}) {
					oldAllowed = true
				}
				if event.Class == "db_column" && reflect.DeepEqual(event.Denied, []string{"relabelto"}) {
					found = true
					if event.Target != sepgsqlP || event.Permissive || !reflect.DeepEqual(event.Required, []string{"relabelto"}) {
						t.Fatal("denial was not solely destination relabelto", event)
					}
				}
			}
			if !found || !oldAllowed || !stagedEarlier {
				t.Fatal("missing genuine old/new/earlier-phase authority evidence", found, oldAllowed, stagedEarlier)
			}
			restoreconDroppedFacts(t, p, c, run, true)
		}
	})
}

func restoreconTransactionConfig(config *stuntgres.SepgsqlSimulationConfig) {
	config.Policy.NamedSpecs["secret"] = strings.ReplaceAll(config.Policy.DefaultSpec, sepgsqlP, sepgsqlS)
	config.Policy.NamedSpecs["denied"] = "db_column * " + sepgsqlT + "\n" + config.Policy.DefaultSpec
	config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlA, Target: sepgsqlT, Class: "db_column", Deny: []string{"relabelto"}})
}

func TestReferenceSepgsqlSimulationRestoreconTransactions(t *testing.T) {
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		restoreconInactive(t, p, backend, c, run)
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT ordinary", 0))
			step := fixture.Exec("UPDATE restorecon_dropped SET tail=10", 1)
			step.Prepared = prepared
			run(step)
			run(fixture.Exec("ROLLBACK TO ordinary", 0))
			run(fixture.Exec("RELEASE ordinary", 0))
			run(fixture.Exec("COMMIT", 0))
			restoreconDroppedFacts(t, p, c, run, false)
		}
	}, restoreconTransactionConfig, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		restoreconDroppedSetup(run)
		for _, prepared := range []bool{false, true} {
			restoreconCall(run, "sepgsql_restorecon", "secret", prepared)
			before := sepgsqlRestoreconLabels(t, p, c)
			run(fixture.Exec("BEGIN", 0))
			restoreconCall(run, "sepgsql_restorecon", nil, prepared)
			restoreconDroppedLabels(run, sepgsqlP)
			run(fixture.Exec("ROLLBACK", 0))
			restoreconUnchanged(t, p, c, before)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT restored", 0))
			restoreconCall(run, "sepgsql_restorecon", nil, prepared)
			restoreconDroppedLabels(run, sepgsqlP)
			run(fixture.Exec("ROLLBACK TO restored", 0))
			restoreconUnchanged(t, p, c, before)
			step := sepgsqlDenied("SELECT sepgsql_restorecon('denied')")
			step.Prepared = prepared
			run(step)
			run(fixture.Exec("ROLLBACK TO restored", 0))
			restoreconUnchanged(t, p, c, before)
			restoreconCall(run, "sepgsql_restorecon", nil, prepared)
			run(fixture.Exec("RELEASE restored", 0))
			run(fixture.Exec("COMMIT", 0))
			restoreconDroppedLabels(run, sepgsqlP)
			restoreconDroppedFacts(t, p, c, run, true)
		}
	})
}
