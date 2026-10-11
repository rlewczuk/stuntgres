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

func TestReferencePGRolesControls(t *testing.T) {
	var dump string
	var pair *fixture.Pair
	var postgres fixture.Backend
	pgRolesScenario(t, []string{"pgr_controls", "pgr_control_member"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pair = p
		if b.PG {
			postgres = b
		}
		id := pgRolesCreate(t, p, b, "pgr_controls")
		pgRolesExec(t, p, b, "ALTER ROLE pgr_controls CREATEDB")
		pgRolesCreate(t, p, b, "pgr_control_member")
		pgRolesExec(t, p, b, "GRANT pgr_controls TO pgr_control_member WITH SET TRUE")
		pgRolesExec(t, p, b, "CREATE TABLE pgr_control_sentinel(a INT)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgr_control_sentinel VALUES(7)", 1))
		pgRolesExec(t, p, b, "CREATE VIEW pgr_control_view AS SELECT r.rolname,r.rolcreatedb,r.rolpassword,r.rolconfig,s.a FROM pg_catalog.pg_roles r JOIN pgr_control_sentinel s ON s.a=7 WHERE r.rolname='pgr_controls'")
		check := func() {
			t.Helper()
			row := pgRolesRow("pgr_controls", id, nil)
			row[4] = true
			pgRolesBoth(t, p, b, pgRolesSelect("SELECT * FROM pg_roles WHERE oid=$1", [][]any{row}, id))
			pgRolesBoth(t, p, b, fixture.Select("SELECT * FROM pgr_control_view", []string{"rolname", "rolcreatedb", "rolpassword", "rolconfig", "a"}, []string{"NAME", "BOOL", "TEXT", "_TEXT", "INT4"}, [][]any{{"pgr_controls", true, "********", nil, int64(7)}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a FROM pgr_control_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		membership := func() {
			t.Helper()
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			for _, q := range []string{"SET SESSION AUTHORIZATION pgr_control_member", "SET ROLE pgr_controls", "RESET ROLE", "RESET SESSION AUTHORIZATION"} {
				if _, err := conn.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
		}
		check()
		old, err := b.DB.QueryContext(p.Ctx, "SELECT rolcreatedb FROM pg_roles WHERE oid=$1", id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if !b.PG {
			// A recognizable credential is never printed, including on control failures.
			if _, err := b.DB.ExecContext(p.Ctx, "ALTER ROLE pgr_controls PASSWORD 'pgu-control-private-password'"); err != nil {
				t.Fatal("password fixture setup failed")
			}
			dump = commentInquiryDump(t, p)
			for _, forbidden := range []string{"CREATE ROLE", "pgu-control-private-password", "SCRAM-SHA-256$", "INSERT INTO \"pg_catalog\".\"pg_roles\""} {
				if strings.Contains(dump, forbidden) {
					t.Fatal("credential or cluster state leaked into Dump")
				}
			}
			badPrefix := dump + "INSERT INTO pgr_control_sentinel VALUES("
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(badPrefix+"'bad');")); err == nil {
				t.Fatal("bad staged Restore succeeded")
			} else {
				want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
				want.Position = fixture.Position(utf8.RuneCountInString(badPrefix) + 1)
				fixture.AssertDiagnostic(t, err, &want)
			}
			check()
			if commentInquiryDump(t, p) != dump {
				t.Fatal("failed Restore changed catalog")
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Reset(canceled, p.Native); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Dump(canceled, p.Native, &strings.Builder{}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(dump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			row := pgRolesRow("pgr_controls", id, nil)
			row[4] = true
			pgRolesBoth(t, p, b, pgRolesSelect("SELECT * FROM pg_roles WHERE oid=$1", [][]any{row}, id))
			membership()
			pgRolesBoth(t, p, b, fixture.Select("SELECT relname FROM pg_class WHERE relname='pgr_control_view' OR relname='pgr_control_sentinel'", []string{"relname"}, []string{"NAME"}, [][]any{}))
			pgRolesBoth(t, p, b, fixture.Select("SELECT relkind,relnatts FROM pg_class WHERE relname='pg_roles'", []string{"relkind", "relnatts"}, []string{"CHAR", "INT2"}, [][]any{{"v", int64(13)}}))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check()
			membership()
		}
		pgUserRetainedFlag(t, old, true)
		if len(metadata) != 1 || metadata[0].Name() != "rolcreatedb" || metadata[0].DatabaseTypeName() != "BOOL" {
			t.Fatal("control replaced retained metadata")
		}
		// View does not own/depend on the selected cluster role.
		if !b.PG {
			pgRolesExec(t, p, b, "DROP ROLE pgr_control_member")
			pgRolesExec(t, p, b, "DROP ROLE pgr_controls")
		}
	})
	if dump != "" && postgres.DB != nil {
		for _, q := range []string{"DROP VIEW pgr_control_view", "DROP TABLE pgr_control_sentinel", dump} {
			if _, err := postgres.DB.ExecContext(pair.Ctx, q); err != nil {
				t.Fatal("PG18 rejected actual emitted local SQL")
			}
		}
		pgRolesBoth(t, pair, postgres, fixture.Select("SELECT * FROM pgr_control_view", []string{"rolname", "rolcreatedb", "rolpassword", "rolconfig", "a"}, []string{"NAME", "BOOL", "TEXT", "_TEXT", "INT4"}, [][]any{{"pgr_controls", true, "********", nil, int64(7)}}))
	}
	if postgres.DB != nil {
		pgRolesExec(t, pair, postgres, "DROP ROLE pgr_control_member")
		pgRolesExec(t, pair, postgres, "DROP ROLE pgr_controls")
	}
}
