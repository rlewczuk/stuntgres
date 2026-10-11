package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferencePGGroupControls(t *testing.T) {
	var dump string
	var pair *fixture.Pair
	var postgres fixture.Backend
	var pgGroupID, pgMemberID int64
	pgGroupScenario(t, []string{"pgg_control", "pgg_control_member"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pair = p
		id := pgGroupCreate(t, p, b, "pgg_control", "NOLOGIN")
		member := pgGroupCreate(t, p, b, "pgg_control_member", "LOGIN")
		if b.PG {
			postgres, pgGroupID, pgMemberID = b, id, member
		}
		pgRolesExec(t, p, b, "GRANT pgg_control TO pgg_control_member WITH ADMIN FALSE, INHERIT FALSE, SET FALSE")
		if _, err := b.DB.ExecContext(p.Ctx, "ALTER ROLE pgg_control PASSWORD 'group-control-private-password'"); err != nil {
			t.Fatal("credential setup failed")
		}
		pgRolesExec(t, p, b, "CREATE TABLE pgg_control_sentinel(a INT)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgg_control_sentinel VALUES(7)", 1))
		pgRolesExec(t, p, b, "CREATE VIEW pgg_control_view AS SELECT g.groname,g.grosysid,g.grolist FROM pg_catalog.pg_group g JOIN pgg_control_sentinel s ON s.a=7 WHERE g.groname='pgg_control'")
		check := func() {
			pgGroupBoth(t, p, b, "SELECT * FROM pg_catalog.pg_group WHERE grosysid=$1", [][]any{pgGroupRow("pgg_control", id, member)}, id)
			pgGroupBoth(t, p, b, "SELECT * FROM pgg_control_view", [][]any{pgGroupRow("pgg_control", id, member)})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a FROM pgg_control_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		check()
		if !b.PG {
			// Process-local controls and retained native cursors have no
			// PostgreSQL driver-engine API analogue.
			q := "SELECT * FROM pg_group WHERE grosysid=$1"
			old, record, meta := pgGroupOpenRetained(t, p, b, q, id)
			defer old.Close()
			dump = commentInquiryDump(t, p)
			for _, forbidden := range []string{"CREATE ROLE", "CREATE USER", "CREATE GROUP", "group-control-private-password", "SCRAM-SHA-256$", "INSERT INTO \"pg_catalog\".\"pg_group\""} {
				if strings.Contains(dump, forbidden) {
					t.Fatal("cluster state or credential leaked into Dump")
				}
			}
			if !strings.Contains(dump, "pg_group") || !strings.Contains(dump, "pgg_control_view") {
				t.Fatal("Dump lost symbolic group consumer")
			}
			prefix := dump + "INSERT INTO pgg_control_sentinel VALUES("
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+"'bad');"))
			want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
			want.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
			fixture.AssertDiagnostic(t, err, &want)
			check()
			if commentInquiryDump(t, p) != dump {
				t.Fatal("failed staged Restore changed catalog")
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Reset(canceled, p.Native); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled Reset", err)
			}
			if err := stuntgres.Dump(canceled, p.Native, &strings.Builder{}); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled Dump", err)
			}
			if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(dump)); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled Restore", err)
			}
			check()
			if commentInquiryDump(t, p) != dump {
				t.Fatal("canceled controls changed catalog")
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			pgGroupBoth(t, p, b, q, [][]any{pgGroupRow("pgg_control", id, member)}, id)
			pgRolesBoth(t, p, b, fixture.Select("SELECT relname FROM pg_class WHERE relname='pgg_control_view' OR relname='pgg_control_sentinel'", []string{"relname"}, []string{"NAME"}, nil))
			pgRolesBoth(t, p, b, fixture.Select("SELECT relkind,relnatts FROM pg_class WHERE relname='pg_group'", []string{"relkind", "relnatts"}, []string{"CHAR", "INT2"}, [][]any{{"v", int64(3)}}))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check()
			pgGroupConsume(t, old, b, q, [][]any{pgGroupRow("pgg_control", id, member)}, record)
			pgGroupSavedMetadata(t, meta, false)
			// Roles remain cluster state even when the user catalog is replaced.
			pgRolesExec(t, p, b, "REVOKE pgg_control FROM pgg_control_member")
			pgGroupBoth(t, p, b, "SELECT * FROM pgg_control_view", [][]any{pgGroupRow("pgg_control", id)})
		}
	})
	// Replay actual emitted native SQL, never a substitute PostgreSQL Reset API.
	// Backend-local role identities deliberately differ from the native allocator.
	if dump != "" && postgres.DB != nil {
		for _, q := range []string{"DROP VIEW pgg_control_view", "DROP TABLE pgg_control_sentinel", dump} {
			if _, err := postgres.DB.ExecContext(pair.Ctx, q); err != nil {
				t.Fatal("pinned PostgreSQL rejected actual emitted SQL", err)
			}
		}
		pgGroupBoth(t, pair, postgres, "SELECT * FROM pgg_control_view", [][]any{pgGroupRow("pgg_control", pgGroupID, pgMemberID)})
		fixture.RunStep(t, pair.Ctx, postgres.DB, postgres.Recorder, fixture.Select("SELECT a FROM pgg_control_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
}
