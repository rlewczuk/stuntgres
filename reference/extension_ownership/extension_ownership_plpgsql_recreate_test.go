package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Independently specified by REL_18_6 src/pl/plpgsql/src/plpgsql--1.0.sql:
// all three declarations use MODULE_PATHNAME ($libdir/plpgsql), default VOLATILE
// and PARALLEL UNSAFE; only the inline handler and validator are STRICT.
func TestReferenceExtensionOwnershipPLpgSQLRecreate(t *testing.T) {
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		// Stock objects are cluster-global on the oracle. Restore the released
		// installation even if a failure occurs between DROP and CREATE.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, q := range []string{
				`DROP PROCEDURE IF EXISTS public.eo_pl_recreated(integer)`,
				`DROP EXTENSION IF EXISTS plpgsql CASCADE`,
				`CREATE EXTENSION plpgsql`,
			} {
				if _, err := c.ExecContext(ctx, q); err != nil {
					t.Error("restoring released plpgsql", q, err)
				}
			}
		}()
		metadata := func(t *testing.T, conn *sql.Conn, recorder *fixture.Recorder, altered bool) {
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			parallel, cost := "u", float64(1)
			if altered {
				parallel, cost = "r", float64(7.25)
			}
			s := fixture.Select(`SELECT p.proname,p.proargtypes::text AS arguments,p.prorettype::bigint,p.prolang=l.oid AS c_language,p.prosrc,p.probin,p.proparallel,p.proisstrict,p.provolatile,p.procost FROM pg_proc p JOIN pg_language l ON l.lanname='c' WHERE p.pronamespace='pg_catalog'::regnamespace AND p.proname IN ('plpgsql_call_handler','plpgsql_inline_handler','plpgsql_validator') ORDER BY p.proname`,
				[]string{"proname", "arguments", "prorettype", "c_language", "prosrc", "probin", "proparallel", "proisstrict", "provolatile", "procost"},
				[]string{"NAME", "TEXT", "INT8", "BOOL", "TEXT", "TEXT", "CHAR", "BOOL", "CHAR", "FLOAT4"},
				[][]any{
					{"plpgsql_call_handler", "", int64(2280), true, "plpgsql_call_handler", "$libdir/plpgsql", parallel, false, "v", cost},
					{"plpgsql_inline_handler", "2281", int64(2278), true, "plpgsql_inline_handler", "$libdir/plpgsql", "u", true, "v", float64(1)},
					{"plpgsql_validator", "26", int64(2278), true, "plpgsql_validator", "$libdir/plpgsql", "u", true, "v", float64(1)},
				})
			r(s)
			r(fixture.Prepared(s))
			r(fixture.Select(`SELECT p.proname,d.deptype FROM pg_depend d JOIN pg_language l ON d.classid='pg_language'::regclass AND d.objid=l.oid JOIN pg_proc p ON d.refclassid='pg_proc'::regclass AND d.refobjid=p.oid WHERE l.lanname='plpgsql' ORDER BY p.proname`, []string{"proname", "deptype"}, []string{"NAME", "CHAR"}, [][]any{{"plpgsql_call_handler", "n"}, {"plpgsql_inline_handler", "n"}, {"plpgsql_validator", "n"}}))
			r(fixture.Select(`SELECT d.classid::bigint,count(*) FROM pg_depend d JOIN pg_extension e ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid WHERE e.extname='plpgsql' AND d.deptype='e' GROUP BY d.classid ORDER BY d.classid`, []string{"classid", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(1255), int64(3)}, {int64(2612), int64(1)}}))
			r(ownershipCount(`SELECT count(*) FROM pg_language WHERE lanname='plpgsql' AND lanpltrusted AND lanplcallfoid='pg_catalog.plpgsql_call_handler()'::regprocedure AND laninline='pg_catalog.plpgsql_inline_handler(internal)'::regprocedure AND lanvalidator='pg_catalog.plpgsql_validator(oid)'::regprocedure`, 1))
		}
		metadata(t, c, b.Recorder, false)
		run(fixture.Exec(`DROP EXTENSION plpgsql CASCADE`, 0))
		run(ownershipCount(`SELECT count(*) FROM pg_language WHERE lanname='plpgsql'`, 0))
		run(ownershipCount(`SELECT count(*) FROM pg_proc WHERE pronamespace='pg_catalog'::regnamespace AND proname IN ('plpgsql_call_handler','plpgsql_inline_handler','plpgsql_validator')`, 0))
		run(fixture.Exec(`CREATE EXTENSION plpgsql`, 0))
		metadata(t, c, b.Recorder, false)
		run(fixture.Exec(`CREATE PROCEDURE public.eo_pl_recreated(INOUT n integer) LANGUAGE plpgsql AS 'BEGIN n:=n+1; END'`, 0))
		run(fixture.Exec(`ALTER FUNCTION pg_catalog.plpgsql_call_handler() COST 7.25 PARALLEL RESTRICTED`, 0))
		check := func(t *testing.T, conn *sql.Conn, recorder *fixture.Recorder) {
			metadata(t, conn, recorder, true)
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			s := fixture.Select(`CALL public.eo_pl_recreated(4)`, []string{"n"}, []string{"INT4"}, [][]any{{int64(5)}})
			r(s)
			r(fixture.Prepared(s))
			r(fixture.Exec(`DO LANGUAGE plpgsql 'DECLARE n integer := 1; BEGIN n := n + 1; END'`, 0))
		}
		check(t, c, b.Recorder)
		if b.PG {
			return
		}
		saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
		const current = `SELECT p.proname,pg_get_userbyid(p.proowner),p.proacl::text,p.proparallel,p.procost,p.probin FROM pg_proc p WHERE p.pronamespace='pg_catalog'::regnamespace AND p.proname IN ('plpgsql_call_handler','plpgsql_inline_handler','plpgsql_validator') ORDER BY p.proname`
		before := ownershipSnapshot(t, p, c, current)
		oldRows, err := c.QueryContext(p.Ctx, `SELECT probin FROM pg_proc WHERE oid='pg_catalog.plpgsql_call_handler()'::regprocedure`)
		if err != nil {
			t.Fatal(err)
		}
		defer oldRows.Close()
		connector, err := stuntgres.NewDriver().OpenConnector("eo_plpgsql_recreate")
		if err != nil {
			t.Fatal(err)
		}
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		independent := sql.OpenDB(connector)
		defer independent.Close()
		for _, destination := range []struct {
			name              string
			control, recorded *sql.DB
			recorder          *fixture.Recorder
		}{
			{"same_driver", p.Native, b.DB, b.Recorder},
			{"independent_driver", independent, recorded, recorder},
		} {
			t.Run(destination.name, func(t *testing.T) {
				if err := stuntgres.Reset(p.Ctx, destination.control); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, destination.control, strings.NewReader(saved)); err != nil {
					t.Fatal(err)
				}
				conn, err := destination.recorded.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				check(t, conn, destination.recorder)
				ownershipUnchanged(t, p, conn, current, before)
				if destination.name == "same_driver" {
					var library string
					if !oldRows.Next() {
						t.Fatal("lost retained handler row", oldRows.Err())
					}
					if err := oldRows.Scan(&library); err != nil || library != "$libdir/plpgsql" {
						t.Fatal("retained handler library changed", library, err)
					}
					if oldRows.Next() || oldRows.Err() != nil {
						t.Fatal("retained handler row multiplicity", oldRows.Err())
					}
					if err := oldRows.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if again := fixture.ExtensionDump(t, p.Ctx, destination.control); again != saved {
					t.Fatal("recreated plpgsql changed on redump")
				}
			})
		}
	})
}
