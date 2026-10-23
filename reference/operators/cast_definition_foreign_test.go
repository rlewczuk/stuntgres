package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Metadata-only foreign definitions use portable wrapper/server SQL, so the
// actual native dump can also execute on PostgreSQL without remote providers.
// Foreign defaults are witnessed through their actual retained routine
// dependencies; ordinary defaults additionally execute to independent values.
func TestReferenceCastDefinitionForeignDefaults(t *testing.T) {
	p := fixture.NewScenario(t)
	// Global wrappers and non-public schemas outlive the fixture's public reset.
	// Use the borrowed owner and a fresh deadline even after a failed assertion.
	reset := func(c *sql.Conn, public bool) error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		queries := []string{
			"ROLLBACK",
			"RESET ROLE",
			"RESET SESSION AUTHORIZATION",
			"RESET search_path",
			"DROP FOREIGN DATA WRAPPER IF EXISTS fdw_meta CASCADE",
			"DROP SCHEMA IF EXISTS fdw_import CASCADE",
		}
		if public {
			queries = append(queries, "DROP SCHEMA public CASCADE; CREATE SCHEMA public")
		}
		for _, q := range queries {
			if _, err := c.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	}
	body := func(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta NO HANDLER NO VALIDATOR", 0))
		run(fixture.Exec("CREATE SERVER fdw_server FOREIGN DATA WRAPPER fdw_meta", 0))
		for _, q := range []string{
			"CREATE SCHEMA fdw_import",
			"CREATE TYPE fdw_import.cast_src AS ENUM ('red')",
			"CREATE FUNCTION fdw_import.cast_old(fdw_import.cast_src) RETURNS integer LANGUAGE SQL AS $$ SELECT 41 $$",
			"CREATE FUNCTION fdw_import.cast_new(fdw_import.cast_src) RETURNS integer LANGUAGE SQL AS $$ SELECT 77 $$",
			"CREATE FUNCTION fdw_import.cast_late() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT 0; END",
			"CREATE CAST (fdw_import.cast_src AS integer) WITH FUNCTION fdw_import.cast_old(fdw_import.cast_src) AS IMPLICIT",
		} {
			run(fixture.Exec(q, 0))
		}
		// The simple CASE comparison is emitted as original syntax and needs
		// the actual selected implicit edge, unlike a normalized scalar call.
		value := "CASE 'red'::fdw_import.cast_src WHEN 41 THEN 1 WHEN 77 THEN 2 ELSE 0 END"
		for _, epoch := range []string{"old", "fresh"} {
			if epoch == "fresh" {
				run(fixture.Exec("DROP CAST (fdw_import.cast_src AS integer)", 0))
				run(fixture.Exec("CREATE CAST (fdw_import.cast_src AS integer) WITH FUNCTION fdw_import.cast_new(fdw_import.cast_src) AS IMPLICIT", 0))
			}
			for _, phase := range []string{"inline", "late"} {
				definition := value
				if phase == "late" {
					definition = "(" + value + ")+fdw_import.cast_late()"
				}
				run(fixture.Exec("CREATE FOREIGN TABLE fdw_import.cast_"+epoch+"_"+phase+"(id integer DEFAULT "+definition+") SERVER fdw_server", 0))
			}
			run(fixture.Exec("CREATE TABLE fdw_import.cast_"+epoch+"_ordinary(id integer DEFAULT "+value+")", 0))
			if epoch == "old" {
				run(fixture.Exec("CREATE FOREIGN TABLE fdw_import.cast_mixed(a integer DEFAULT "+value+",b integer) SERVER fdw_server", 0))
			} else {
				run(fixture.Exec("ALTER FOREIGN TABLE fdw_import.cast_mixed ALTER COLUMN b SET DEFAULT "+value, 0))
			}
		}
		check := func(run func(fixture.Step)) {
			t.Helper()
			// Exercise real ordinary defaults and independently assert which
			// routine each foreign declaration retained without remote writes.
			for _, epoch := range []string{"old", "fresh"} {
				want := int64(1)
				routine := "cast_old"
				if epoch == "fresh" {
					want = 2
					routine = "cast_new"
				}
				run(fixture.Exec("INSERT INTO fdw_import.cast_"+epoch+"_ordinary DEFAULT VALUES", 1))
				run(fixture.Select("SELECT id FROM fdw_import.cast_"+epoch+"_ordinary", []string{"id"}, []string{"INT4"}, [][]any{{want}}))
				run(fixture.Exec("DELETE FROM fdw_import.cast_"+epoch+"_ordinary", 1))
				for _, phase := range []string{"inline", "late"} {
					// The retained default owns its selected routine, not a
					// dependency on today's replaceable pg_cast row.
					run(fixture.Select("SELECT p.proname FROM pg_attrdef d JOIN pg_depend x ON x.objid=d.oid JOIN pg_proc p ON p.oid=x.refobjid WHERE d.adrelid='fdw_import.cast_"+epoch+"_"+phase+"'::regclass AND x.classid=2604 AND x.refclassid=1255 AND p.proname IN ('cast_old','cast_new') ORDER BY p.proname", []string{"proname"}, []string{"NAME"}, [][]any{{routine}}))
				}
			}
			run(fixture.Select("SELECT a.attname,p.proname FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum JOIN pg_depend x ON x.objid=d.oid JOIN pg_proc p ON p.oid=x.refobjid WHERE d.adrelid='fdw_import.cast_mixed'::regclass AND x.classid=2604 AND x.refclassid=1255 AND p.proname IN ('cast_old','cast_new') ORDER BY a.attnum", []string{"attname", "proname"}, []string{"NAME", "NAME"}, [][]any{{"a", "cast_old"}, {"b", "cast_new"}}))
			run(fixture.Select("SELECT castcontext,castmethod,castfunc::regprocedure::text AS routine FROM pg_cast WHERE castsource='fdw_import.cast_src'::regtype::oid AND casttarget='integer'::regtype::oid", []string{"castcontext", "castmethod", "routine"}, []string{"CHAR", "CHAR", "TEXT"}, [][]any{{"i", "f", "fdw_import.cast_new(fdw_import.cast_src)"}}))
			run(fixture.Select("SELECT ('red'::fdw_import.cast_src)::integer AS fresh", []string{"fresh"}, []string{"INT4"}, [][]any{{int64(77)}}))
			// Replay helpers must be gone from the actual catalog, not merely
			// absent from a particular rendering of the emitted script.
			run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='fdw_import.cast_src'::regtype::oid OR casttarget='fdw_import.cast_src'::regtype::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='fdw_import' ORDER BY p.proname", []string{"proname"}, []string{"NAME"}, [][]any{{"cast_late"}, {"cast_new"}, {"cast_old"}}))
			run(fixture.Select("SELECT w.fdwhandler=0 AS no_handler,w.fdwvalidator=0 AS no_validator FROM pg_foreign_data_wrapper w JOIN pg_foreign_server s ON s.srvfdw=w.oid WHERE w.fdwname='fdw_meta' AND s.srvname='fdw_server'", []string{"no_handler", "no_validator"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		}
		check(run)
		if b.PG {
			return
		} // Only native has Dump/Reset/Restore APIs.
		var snapshot bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(run)
		if p.Postgres != nil {
			pg, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pg.Close()
			// Reset even on a failed replay/assertion, before the later
			// independent PostgreSQL setup uses the shared oracle. LIFO defers
			// clean the borrowed owner before returning it to the pool.
			defer func() {
				if err := reset(pg, true); err != nil {
					t.Error("PostgreSQL replay cleanup:", err)
				}
			}()
			if err := reset(pg, true); err != nil {
				t.Fatal("PostgreSQL replay reset:", err)
			}
			if _, err := pg.ExecContext(p.Ctx, snapshot.String()); err != nil {
				t.Fatalf("native SQL replay on PostgreSQL: %v", err)
			}
			check(func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, s)
			})
		}
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer func() {
				if !b.PG {
					if err := c.Close(); err != nil {
						t.Error("foreign cast owner close:", err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					if err := stuntgres.Reset(ctx, p.Native); err != nil {
						t.Error("foreign cast native cleanup:", err)
					}
					return
				}
				if err := reset(c, false); err != nil {
					t.Error("foreign cast cleanup:", err)
				}
			}()
			body(t, b, func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		})
	}
}
