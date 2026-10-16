package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionIdentityStatusDefaultTracking(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			var setting string
			if err := a.QueryRowContext(p.Ctx, "SHOW track_commit_timestamp").Scan(&setting); err != nil || setting != "off" {
				t.Fatalf("expected default tracking off: %q %v", setting, err)
			}
			held, err := observer.PrepareContext(p.Ctx, "SELECT pg_xact_status($1::xid8)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, end := range []string{"COMMIT", "ROLLBACK"} {
				fixture.LockExec(t, p.Ctx, a, "BEGIN READ ONLY", 0)
				var absent sql.NullString
				if err := a.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id_if_assigned()::text").Scan(&absent); err != nil || absent.Valid {
					t.Fatalf("read inquiry assigned: %v %v", absent, err)
				}
				var id string
				if err := a.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&id); err != nil {
					t.Fatal(err)
				}
				check := func(want string) {
					t.Helper()
					for _, prepared := range []bool{false, true} {
						var got string
						var err error
						if prepared {
							err = held.QueryRowContext(p.Ctx, id).Scan(&got)
						} else {
							err = observer.QueryRowContext(p.Ctx, "SELECT pg_xact_status($1::xid8)", id).Scan(&got)
						}
						if err != nil || got != want {
							t.Fatalf("tracking-off status %s: %q %v want %q", id, got, err, want)
						}
					}
				}
				check("in progress")
				fixture.LockExec(t, p.Ctx, a, end, 0)
				want := "committed"
				if end == "ROLLBACK" {
					want = "aborted"
				}
				check(want)
			}
		})
	}
}
