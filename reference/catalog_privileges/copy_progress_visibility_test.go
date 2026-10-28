package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

var copyProgressRoles = []string{"copy_worker", "copy_unrelated", "copy_member", "copy_reader", "copy_monitor", "copy_effective", "copy_effective_member"}

func copyProgressSeedRoles(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	admin := vacuumConn(t, p, b)
	for _, role := range copyProgressRoles {
		copyProgressExec(t, p.Ctx, admin, "CREATE ROLE "+role+" LOGIN PASSWORD 'role-core-fixture'")
		copyProgressExec(t, p.Ctx, admin, "GRANT USAGE ON SCHEMA public TO "+role)
	}
	for _, q := range []string{
		"GRANT copy_worker TO copy_member",
		"GRANT copy_unrelated TO copy_member",
		"GRANT pg_read_all_stats TO copy_reader",
		"GRANT pg_monitor TO copy_monitor",
		"GRANT copy_effective TO copy_worker",
		"GRANT copy_effective TO copy_effective_member",
		"CREATE TABLE copy_progress_visibility(id INT)",
		"GRANT INSERT ON copy_progress_visibility TO copy_worker,copy_effective",
		"GRANT SELECT ON copy_progress_visibility TO copy_unrelated",
		"ALTER TABLE copy_progress_visibility OWNER TO copy_unrelated",
	} {
		copyProgressExec(t, p.Ctx, admin, q)
	}
}

func TestReferenceCopyProgressVisibility(t *testing.T) {
	for _, effective := range []bool{false, true} {
		t.Run(map[bool]string{false: "authenticated", true: "worker_set_role"}[effective], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, copyProgressRoles)
			copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				copyProgressSeedRoles(t, p, b)
				admin := vacuumConn(t, p, b)
				worker := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", "copy_worker"))
				id := copyProgressIdentify(t, p, worker, "copy_progress_visibility")
				if effective {
					copyProgressExec(t, p.Ctx, worker, "SET ROLE copy_effective")
				}
				run := copyProgressStart(t, p, b, worker, "COPY copy_progress_visibility FROM STDIN")
				copyProgressAwait(t, p, admin, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
				for _, role := range []string{"copy_worker", "copy_unrelated", "copy_member", "copy_reader", "copy_monitor", "copy_effective_member"} {
					observer := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", role))
					s, ok := copyProgressRead(t, p, observer, id.pid)
					if !ok {
						t.Fatalf("role %s cannot see public row", role)
					}
					if role == "copy_unrelated" || role == "copy_effective_member" {
						copyProgressMasked(t, s, id)
					} else {
						copyProgressVisible(t, s, id, "COPY FROM")
					}
					if role == "copy_member" {
						copyProgressExec(t, p.Ctx, observer, "SET ROLE copy_unrelated")
						s, ok = copyProgressRead(t, p, observer, id.pid)
						if !ok {
							t.Fatal("SET ROLE hid row")
						}
						copyProgressMasked(t, s, id)
						copyProgressExec(t, p.Ctx, observer, "RESET ROLE")
						s, ok = copyProgressRead(t, p, observer, id.pid)
						if !ok {
							t.Fatal("RESET ROLE hid row")
						}
						copyProgressVisible(t, s, id, "COPY FROM")
					}
				}
				s, ok := copyProgressRead(t, p, admin, id.pid)
				if !ok {
					t.Fatal("admin cannot see worker")
				}
				copyProgressVisible(t, s, id, "COPY FROM")
				run.finish(t, 0)
				copyProgressIdle(t, p, admin, id.pid)
			})
		})
	}
}

func TestReferenceCopyProgressFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, copyProgressRoles)
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		copyProgressSeedRoles(t, p, b)
		admin := vacuumConn(t, p, b)
		worker := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", "copy_worker"))
		unrelated := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", "copy_unrelated"))
		id := copyProgressIdentify(t, p, worker, "copy_progress_visibility")
		run := copyProgressStart(t, p, b, worker, "COPY copy_progress_visibility FROM STDIN")
		previous := copyProgressAwait(t, p, admin, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
		copyProgressVisible(t, previous, id, "COPY FROM")
		var supplied int64
		for segment := range 2 {
			payload := copyProgressIntegers(1000+segment*1000, 1000)
			supplied += int64(len(payload))
			run.input.send(t, p, payload)
			s := copyProgressAwait(t, p, admin, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok && s.processed.Int64 == int64((segment+1)*1000) })
			copyProgressVisible(t, s, id, "COPY FROM")
			if s.bytes.Int64 < previous.bytes.Int64 || s.bytes.Int64 > supplied || s.processed.Int64 <= previous.processed.Int64 {
				t.Fatalf("smoke counters %+v previous %+v", s, previous)
			}
			previous = s
		}
		s, ok := copyProgressRead(t, p, unrelated, id.pid)
		if !ok {
			t.Fatal("unrelated observer lost public row")
		}
		copyProgressMasked(t, s, id)
		run.finish(t, 2000)
		copyProgressIdle(t, p, admin, id.pid)
		vacuumRun(t, p, b, admin, fixture.Select("SELECT count(*) AS n FROM copy_progress_visibility", []string{"n"}, []string{"INT8"}, [][]any{{int64(2000)}}))
		// A fresh admitted command must disappear on cancellation without adding data.
		canceled := copyProgressStart(t, p, b, worker, "COPY copy_progress_visibility FROM STDIN")
		copyProgressAwait(t, p, admin, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
		copyProgressCancelAdmitted(t, p, b, admin, id, canceled, "copy_progress_visibility")
		copyProgressIdle(t, p, admin, id.pid)
		vacuumRun(t, p, b, admin, fixture.Select("SELECT count(*) AS n FROM copy_progress_visibility", []string{"n"}, []string{"INT8"}, [][]any{{int64(2000)}}))
	})
}
