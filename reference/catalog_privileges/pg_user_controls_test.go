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

func TestReferencePGUserControls(t *testing.T) {
	var dump string
	var pair *fixture.Pair
	var postgres fixture.Backend
	pgUserScenario(t, []string{"pgu_controls"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pair = p
		if b.PG {
			postgres = b
		}
		id := pgUserCreate(t, p, b, "pgu_controls")
		pgUserExec(t, p, b, "ALTER ROLE pgu_controls CREATEDB")
		pgUserExec(t, p, b, "CREATE TABLE pgu_control_sentinel(a INT)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgu_control_sentinel VALUES(7)", 1))
		pgUserExec(t, p, b, "CREATE VIEW pgu_control_view AS SELECT usename,usecreatedb,passwd,useconfig FROM pg_catalog.pg_user WHERE usename='pgu_controls'")
		check := func() {
			t.Helper()
			row := pgUserRow("pgu_controls", id, nil)
			row[2] = true
			pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{row}, id))
			pgUserBoth(t, p, b, fixture.Select("SELECT * FROM pgu_control_view", []string{"usename", "usecreatedb", "passwd", "useconfig"}, []string{"NAME", "BOOL", "TEXT", "_TEXT"}, [][]any{{"pgu_controls", true, "********", nil}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a FROM pgu_control_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		check()
		old, err := b.DB.QueryContext(p.Ctx, "SELECT usecreatedb FROM pg_user WHERE usesysid=$1", id)
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
			if _, err := b.DB.ExecContext(p.Ctx, "ALTER ROLE pgu_controls PASSWORD 'pgu-control-private-password'"); err != nil {
				t.Fatal("password fixture setup failed")
			}
			dump = fixture.CommentInquiryDump(t, p)
			for _, forbidden := range []string{"CREATE ROLE", "pgu-control-private-password", "SCRAM-SHA-256$", "INSERT INTO \"pg_catalog\".\"pg_user\""} {
				if strings.Contains(dump, forbidden) {
					t.Fatal("credential or cluster state leaked into Dump")
				}
			}
			badPrefix := dump + "INSERT INTO pgu_control_sentinel VALUES("
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(badPrefix+"'bad');")); err == nil {
				t.Fatal("bad staged Restore succeeded")
			} else {
				want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
				want.Position = fixture.Position(utf8.RuneCountInString(badPrefix) + 1)
				fixture.AssertDiagnostic(t, err, &want)
			}
			check()
			if fixture.CommentInquiryDump(t, p) != dump {
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
			row := pgUserRow("pgu_controls", id, nil)
			row[2] = true
			pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{row}, id))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check()
		}
		pgUserRetainedFlag(t, old, true)
		if len(metadata) != 1 || metadata[0].Name() != "usecreatedb" || metadata[0].DatabaseTypeName() != "BOOL" {
			t.Fatal("control replaced retained metadata")
		}
		// View does not own/depend on the selected cluster role.
		if !b.PG {
			pgUserExec(t, p, b, "DROP ROLE pgu_controls")
		}
	})
	if dump != "" && postgres.DB != nil {
		for _, q := range []string{"DROP VIEW pgu_control_view", "DROP TABLE pgu_control_sentinel", dump} {
			if _, err := postgres.DB.ExecContext(pair.Ctx, q); err != nil {
				t.Fatal("PG18 rejected actual emitted local SQL")
			}
		}
		pgUserBoth(t, pair, postgres, fixture.Select("SELECT * FROM pgu_control_view", []string{"usename", "usecreatedb", "passwd", "useconfig"}, []string{"NAME", "BOOL", "TEXT", "_TEXT"}, [][]any{{"pgu_controls", true, "********", nil}}))
	}
	if postgres.DB != nil {
		pgUserExec(t, pair, postgres, "DROP ROLE pgu_controls")
	}
}
