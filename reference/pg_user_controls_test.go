package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestReferencePGUserControls(t *testing.T) {
	var dump string
	var pair *referencePair
	var postgres referenceBackend
	pgUserScenario(t, []string{"pgu_controls"}, func(t *testing.T, p *referencePair, b referenceBackend) {
		pair = p
		if b.pg {
			postgres = b
		}
		id := pgUserCreate(t, p, b, "pgu_controls")
		pgUserExec(t, p, b, "ALTER ROLE pgu_controls CREATEDB")
		pgUserExec(t, p, b, "CREATE TABLE pgu_control_sentinel(a INT)")
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO pgu_control_sentinel VALUES(7)", 1))
		pgUserExec(t, p, b, "CREATE VIEW pgu_control_view AS SELECT usename,usecreatedb,passwd,useconfig FROM pg_catalog.pg_user WHERE usename='pgu_controls'")
		check := func() {
			t.Helper()
			row := pgUserRow("pgu_controls", id, nil)
			row[2] = true
			pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{row}, id))
			pgUserBoth(t, p, b, referenceSelect("SELECT * FROM pgu_control_view", []string{"usename", "usecreatedb", "passwd", "useconfig"}, []string{"NAME", "BOOL", "TEXT", "_TEXT"}, [][]any{{"pgu_controls", true, "********", nil}}))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT a FROM pgu_control_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		check()
		old, err := b.db.QueryContext(p.ctx, "SELECT usecreatedb FROM pg_user WHERE usesysid=$1", id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if !b.pg {
			// A recognizable credential is never printed, including on control failures.
			if _, err := b.db.ExecContext(p.ctx, "ALTER ROLE pgu_controls PASSWORD 'pgu-control-private-password'"); err != nil {
				t.Fatal("password fixture setup failed")
			}
			dump = commentInquiryDump(t, p)
			for _, forbidden := range []string{"CREATE ROLE", "pgu-control-private-password", "SCRAM-SHA-256$", "INSERT INTO \"pg_catalog\".\"pg_user\""} {
				if strings.Contains(dump, forbidden) {
					t.Fatal("credential or cluster state leaked into Dump")
				}
			}
			badPrefix := dump + "INSERT INTO pgu_control_sentinel VALUES("
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(badPrefix+"'bad');")); err == nil {
				t.Fatal("bad staged Restore succeeded")
			} else {
				want := controlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
				want.Position = referencePosition(utf8.RuneCountInString(badPrefix) + 1)
				referenceAssertDiagnostic(t, err, &want)
			}
			check()
			if commentInquiryDump(t, p) != dump {
				t.Fatal("failed Restore changed catalog")
			}
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			if err := stuntgres.Reset(canceled, p.native); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Dump(canceled, p.native, &strings.Builder{}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(canceled, p.native, strings.NewReader(dump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check()
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			row := pgUserRow("pgu_controls", id, nil)
			row[2] = true
			pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{row}, id))
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check()
		}
		pgUserRetainedFlag(t, old, true)
		if len(metadata) != 1 || metadata[0].Name() != "usecreatedb" || metadata[0].DatabaseTypeName() != "BOOL" {
			t.Fatal("control replaced retained metadata")
		}
		// View does not own/depend on the selected cluster role.
		if !b.pg {
			pgUserExec(t, p, b, "DROP ROLE pgu_controls")
		}
	})
	if dump != "" && postgres.db != nil {
		for _, q := range []string{"DROP VIEW pgu_control_view", "DROP TABLE pgu_control_sentinel", dump} {
			if _, err := postgres.db.ExecContext(pair.ctx, q); err != nil {
				t.Fatal("PG18 rejected actual emitted local SQL")
			}
		}
		pgUserBoth(t, pair, postgres, referenceSelect("SELECT * FROM pgu_control_view", []string{"usename", "usecreatedb", "passwd", "useconfig"}, []string{"NAME", "BOOL", "TEXT", "_TEXT"}, [][]any{{"pgu_controls", true, "********", nil}}))
	}
	if postgres.db != nil {
		pgUserExec(t, pair, postgres, "DROP ROLE pgu_controls")
	}
}
