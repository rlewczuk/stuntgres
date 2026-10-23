package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRelationOptionsControls(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		relationOptionsSeed(run)
		run(fixture.Exec("ALTER TABLE ro SET(autovacuum_enabled='OFF')", 0))
		run(relationOptionsState("ro", "fillfactor=70", "autovacuum_enabled=OFF"))
		run(relationOptionsRows("ro"))
		if b.PG {
			return
		} // Native controls have no PostgreSQL API analogue.
		const exact = "SET SESSION AUTHORIZATION 'postgres';\n" +
			"CREATE TABLE \"public\".\"ro\" (\"id\" INTEGER, \"payload\" TEXT);\n" +
			"ALTER TABLE \"public\".\"ro\" SET (\"fillfactor\" = E'70', \"autovacuum_enabled\" = E'OFF');\n" +
			"INSERT INTO \"public\".\"ro\" VALUES (1, 'sentinel');\n"
		if got := relationOptionsDump(t, p); got != exact {
			t.Fatalf("isolated dump\ngot: %q\nwant: %q", got, exact)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(exact)); err != nil {
			t.Fatal(err)
		}
		run(relationOptionsRows("ro"))
		run(relationOptionsState("ro", "fillfactor=70", "autovacuum_enabled=OFF"))
		if relationOptionsDump(t, p) != exact {
			t.Fatal("redump not byte-identical")
		}
		// Add independently observed representable metadata before the fuller
		// control roundtrip. The owner remains the authenticated postgres role.
		run(fixture.Exec("ALTER TABLE ro ADD CONSTRAINT ro_positive CHECK(id>0)", 0))
		run(fixture.Exec("COMMENT ON TABLE ro IS 'kept'", 0))
		run(fixture.Exec("ALTER TABLE ro SET(autovacuum_vacuum_scale_factor='0.250',toast.autovacuum_enabled=false)", 0))
		before := relationOptionsDump(t, p)
		const optionLine = `ALTER TABLE "public"."ro" SET ("fillfactor" = E'70', "autovacuum_enabled" = E'OFF', "autovacuum_vacuum_scale_factor" = E'0.250', "toast"."autovacuum_enabled" = E'false');`
		if !strings.Contains(before, optionLine+"\n") || strings.Count(before, " SET (") != 1 {
			t.Fatal("exact retained order/spelling", before)
		}
		state := []fixture.Step{
			relationOptionsRows("ro"),
			relationOptionsState("ro", "fillfactor=70", "autovacuum_enabled=OFF", "autovacuum_vacuum_scale_factor=0.250"),
			fixture.Select("SELECT c.relowner=r.oid AS owned,obj_description(c.oid,'pg_class') AS comment FROM pg_class c JOIN pg_roles r ON r.rolname='postgres' WHERE c.oid='ro'::regclass", []string{"owned", "comment"}, []string{"BOOL", "TEXT"}, [][]any{{true, "kept"}}),
			fixture.Select("SELECT conname,convalidated FROM pg_constraint WHERE conrelid='ro'::regclass ORDER BY conname", []string{"conname", "convalidated"}, []string{"NAME", "BOOL"}, [][]any{{"ro_positive", true}}),
		}
		old, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Rollback()
		var id int
		if err := old.QueryRowContext(p.Ctx, "SELECT id FROM ro").Scan(&id); err != nil || id != 1 {
			t.Fatal(id, err)
		}
		fixture.RowAffected(t, p.Ctx, old, "ALTER TABLE ro SET(fillfactor=90)", prepared, 0)
		if relationOptionsDump(t, p) != before {
			t.Fatal("Dump leaked private option before epoch replacement")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.TransactionOrdinary(t, old.Commit(), "stuntgres: catalog replaced before publication")
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(before)); err != nil {
			t.Fatal(err)
		}
		for _, s := range state {
			run(s)
		}
		if relationOptionsDump(t, p) != before {
			t.Fatal("TOAST intent or metadata lost on replay")
		}
		relationOptionsBadRestore(t, p, before)
		ctx, cancel := context.WithCancel(p.Ctx)
		reader := &relationOptionsCancelReader{first: strings.NewReader(before + "ALTER TABLE \"public\".\"ro\" SET(fillfactor=80);\n"), cancel: cancel}
		err = stuntgres.Restore(ctx, p.Native, reader)
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled replay", err)
		}
		if relationOptionsDump(t, p) != before {
			t.Fatal("canceled replay replaced destination")
		}
		for _, s := range state {
			run(s)
		}
		// Replay actual emitted SQL against the pinned oracle only for facts it
		// can represent: this TEXT table owns a real physical TOAST relation.
		if p.Postgres != nil {
			defer func() {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal("emitted SQL replay cleanup", err)
				}
			}()
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, before); err != nil {
				t.Fatal("emitted SQL replay", err)
			}
			for _, s := range state {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
			}
			pg, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			relationOptionsToastState(t, p, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, pg, prepared, "ro", "autovacuum_enabled=false")
			pg.Close()
		}
	})
}

type relationOptionsCancelReader struct {
	first  *strings.Reader
	cancel context.CancelFunc
}

func (r *relationOptionsCancelReader) Read(dst []byte) (int, error) {
	n, err := r.first.Read(dst)
	if err == io.EOF {
		r.cancel()
		return 0, context.Canceled
	}
	return n, err
}

func TestReferenceRelationOptionsFeatureSmoke(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE ro_smoke(id INT,payload TEXT)", 0))
		run(fixture.Exec("INSERT INTO ro_smoke VALUES(1,'sentinel')", 1))
		run(fixture.Exec("ALTER TABLE ro_smoke SET(fillfactor=70,autovacuum_enabled=false,parallel_workers=2)", 0))
		want := []string{"fillfactor=70", "autovacuum_enabled=false", "parallel_workers=2"}
		run(relationOptionsState("ro_smoke", want...))
		run(relationOptionsRows("ro_smoke"))
		run(relationOptionsError("ALTER TABLE ro_smoke ADD COLUMN extra INT,SET(fillfactor=9)", "22023", `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`))
		run(relationOptionsState("ro_smoke", want...))
		run(relationOptionsRows("ro_smoke"))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER TABLE ro_smoke SET(fillfactor=80)", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(relationOptionsState("ro_smoke", want...))
		run(fixture.Exec("ALTER TABLE ro_smoke RESET(fillfactor,autovacuum_enabled,parallel_workers)", 0))
		run(relationOptionsState("ro_smoke"))
		run(fixture.Exec("ALTER TABLE ro_smoke SET(autovacuum_enabled='OFF',toast.autovacuum_enabled=false)", 0))
		run(relationOptionsState("ro_smoke", "autovacuum_enabled=OFF"))
		relationOptionsToastState(t, p, b, c, prepared, "ro_smoke", "autovacuum_enabled=false")
		if !b.PG {
			dump := relationOptionsDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			if relationOptionsDump(t, p) != dump {
				t.Fatal("smoke redump changed")
			}
			run(relationOptionsState("ro_smoke", "autovacuum_enabled=OFF"))
			run(relationOptionsRows("ro_smoke"))
		}
	})
}
