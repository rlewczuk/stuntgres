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

var ownershipBRINSignatures = []string{
	"brin_minmax_opcinfo(internal)",
	"brin_minmax_add_value(internal,internal,internal,internal)",
	"brin_minmax_consistent(internal,internal,internal)",
	"brin_minmax_union(internal,internal,internal)",
}

// REL_18_6 extension.c ExecAlterExtensionContentsRecurse admits these actual
// pg_proc objects: the referenced extension is not pinned. Membership is not
// permission to delete the pinned functions (dependency.c), nor a scalar ABI.
// Released declarations are independently specified by pg_proc.dat and the
// existing BRIN catalog proof; no INTERNAL-valued SQL invocation is attempted.
func TestReferenceExtensionOwnershipBRINSupport(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"eo_brin_reader"})
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		cleanup := func(conn *sql.Conn) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn.ExecContext(ctx, "ROLLBACK")
			conn.ExecContext(ctx, "RESET ROLE")
			// Never let extension cleanup attempt deletion of pinned routines.
			for _, signature := range ownershipBRINSignatures {
				var attached bool
				q := `SELECT EXISTS(SELECT 1 FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='pg_proc'::regclass AND d.objid=$1::regprocedure AND d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname='unaccent')`
				if err := conn.QueryRowContext(ctx, q, "pg_catalog."+signature).Scan(&attached); err != nil {
					t.Error("BRIN cleanup lookup", err)
					continue
				}
				if attached {
					if _, err := conn.ExecContext(ctx, "ALTER EXTENSION unaccent DROP FUNCTION pg_catalog."+signature); err != nil {
						t.Error("BRIN cleanup detach", err)
						return
					}
				}
			}
		}
		defer cleanup(c)
		ownershipSetup(run)
		run(fixture.Exec(`CREATE ROLE eo_brin_reader`, 0))
		run(fixture.Exec(`CREATE TABLE eo_old.brin_values(v integer)`, 0))
		run(fixture.Exec(`INSERT INTO eo_old.brin_values VALUES (1),(2),(3),(NULL)`, 4))
		run(fixture.Exec(`CREATE INDEX brin_values_idx ON eo_old.brin_values USING brin(v) WITH (pages_per_range=1)`, 0))
		const declarations = `SELECT oid::bigint,pronamespace::bigint,proowner::bigint,proname,proargtypes::text,prorettype::bigint,prolang::bigint,prosrc,probin,proacl::text,proparallel,procost FROM pg_proc WHERE oid IN (3383,3384,3385,3386) ORDER BY oid`
		const normal = `SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid='pg_proc'::regclass AND objid IN (3383,3384,3385,3386) AND deptype<>'e' ORDER BY objid,refclassid,refobjid,deptype`
		original := ownershipSnapshot(t, p, c, declarations)
		normalBefore := ownershipSnapshot(t, p, c, normal)
		baseline := ownershipSnapshot(t, p, c, ownershipClassMemberSet)
		check := func(t *testing.T, conn *sql.Conn, recorder *fixture.Recorder, attached bool) {
			r := ownershipRunWithRecorder(t, p, conn, recorder)
			n := int64(0)
			if attached {
				n = 1
			}
			ownershipMemberCount(r, 6+4*n)
			for i, signature := range ownershipBRINSignatures {
				r(ownershipCount(`SELECT count(*) FROM pg_proc WHERE oid=$1 AND oid=$2::regprocedure AND pronamespace='pg_catalog'::regnamespace AND proowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)`, 1, int64(3383+i), "pg_catalog."+signature))
				r(ownershipCount(`SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='pg_proc'::regclass AND d.objid=$1 AND d.objsubid=0 AND d.refclassid='pg_extension'::regclass AND d.refobjsubid=0 AND d.deptype='e' AND e.extname='unaccent'`, n, int64(3383+i)))
				r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=$1 AND deptype='e'`, n, int64(3383+i)))
			}
			s := fixture.Select(`SELECT proname,proargtypes::text AS arguments,prorettype::bigint,proisstrict,provolatile,proparallel,proretset,prokind,prolang::bigint,prosrc,probin FROM pg_proc WHERE oid IN (3383,3384,3385,3386) ORDER BY oid`, []string{"proname", "arguments", "prorettype", "proisstrict", "provolatile", "proparallel", "proretset", "prokind", "prolang", "prosrc", "probin"}, []string{"NAME", "TEXT", "INT8", "BOOL", "CHAR", "CHAR", "BOOL", "CHAR", "INT8", "TEXT", "TEXT"}, [][]any{
				{"brin_minmax_opcinfo", "2281", int64(2281), true, "i", "s", false, "f", int64(12), "brin_minmax_opcinfo", nil},
				{"brin_minmax_add_value", "2281 2281 2281 2281", int64(16), true, "i", "s", false, "f", int64(12), "brin_minmax_add_value", nil},
				{"brin_minmax_consistent", "2281 2281 2281", int64(16), true, "i", "s", false, "f", int64(12), "brin_minmax_consistent", nil},
				{"brin_minmax_union", "2281 2281 2281", int64(16), true, "i", "s", false, "f", int64(12), "brin_minmax_union", nil},
			})
			r(s)
			r(fixture.Prepared(s))
			ownershipUnchanged(t, p, conn, declarations, original)
			ownershipUnchanged(t, p, conn, normal, normalBefore)
			s = fixture.Select(`SELECT v FROM eo_old.brin_values WHERE v>=2 ORDER BY v`, []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
			r(s)
			r(fixture.Prepared(s))
			r(ownershipCount(`SELECT count(*) FROM eo_old.brin_values WHERE v IS NULL`, 1))
		}
		check(t, c, b.Recorder, false)
		for _, signature := range ownershipBRINSignatures {
			run(fixture.Exec("ALTER EXTENSION unaccent ADD FUNCTION pg_catalog."+signature, 0))
			name := strings.SplitN(signature, "(", 2)[0]
			run(ownershipError("ALTER EXTENSION unaccent ADD FUNCTION pg_catalog."+signature, "55000", `function `+signature+` is already a member of extension "unaccent"`))
			run(ownershipError("ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog."+signature, "55000", `function `+signature+` is not a member of extension "plpgsql"`))
			run(fixture.Exec(`SET ROLE eo_brin_reader`, 0))
			run(ownershipError("ALTER EXTENSION unaccent DROP FUNCTION pg_catalog."+signature, "42501", "must be owner of extension unaccent"))
			run(fixture.Exec(`RESET ROLE`, 0))
			// Pin protection remains stronger than extension membership removal.
			run(ownershipError("DROP FUNCTION pg_catalog."+signature, "2BP01", "cannot drop function "+name+signature[len(name):]+" because it is required by the database system"))
		}
		check(t, c, b.Recorder, true)
		// A fresh actual class binding while all four capabilities are members.
		run(fixture.Exec(`CREATE INDEX brin_values_member_idx ON eo_old.brin_values USING brin(v)`, 0))
		for _, signature := range ownershipBRINSignatures {
			run(fixture.Exec("ALTER EXTENSION unaccent DROP FUNCTION pg_catalog."+signature, 0))
		}
		check(t, c, b.Recorder, false)
		ownershipUnchanged(t, p, c, ownershipClassMemberSet, baseline)
		// Owning a genuine receiving extension is not ownership of a seeded
		// routine. Trusted installation leaves these pg_catalog owners alone.
		var database string
		if err := c.QueryRowContext(p.Ctx, `SELECT current_database()`).Scan(&database); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("GRANT CREATE ON DATABASE "+extensionTempIdentifier(database)+" TO eo_brin_reader", 0))
		run(fixture.Exec(`GRANT USAGE,CREATE ON SCHEMA eo_other TO eo_brin_reader`, 0))
		// Revoke the database grant even when a later assertion fails, so the
		// fixture's role cleanup never inherits a database ownership dependency.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c.ExecContext(ctx, `RESET ROLE`)
			c.ExecContext(ctx, "REVOKE CREATE ON DATABASE "+extensionTempIdentifier(database)+" FROM eo_brin_reader")
		}()
		run(fixture.Exec(`SET ROLE eo_brin_reader`, 0))
		run(fixture.Exec(`CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`, 0))
		for _, signature := range ownershipBRINSignatures {
			name := strings.SplitN(signature, "(", 2)[0]
			run(ownershipError(`ALTER EXTENSION "uuid-ossp" ADD FUNCTION pg_catalog.`+signature, "42501", "must be owner of function pg_catalog."+name))
		}
		run(fixture.Exec(`DROP EXTENSION "uuid-ossp"`, 0))
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Exec("REVOKE CREATE ON DATABASE "+extensionTempIdentifier(database)+" FROM eo_brin_reader", 0))
		ownershipUnchanged(t, p, c, ownershipClassMemberSet, baseline)
		for _, signature := range ownershipBRINSignatures {
			run(fixture.Exec("ALTER EXTENSION unaccent ADD FUNCTION pg_catalog."+signature, 0))
		}
		if b.PG {
			return
		}
		saved := extensionDump(t, p.Ctx, p.Native)
		oldRows, err := c.QueryContext(p.Ctx, `SELECT proname FROM pg_proc WHERE oid=3383`)
		if err != nil {
			t.Fatal(err)
		}
		defer oldRows.Close()
		connector, err := stuntgres.NewDriver().OpenConnector("eo_brin_support")
		if err != nil {
			t.Fatal(err)
		}
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		independent := sql.OpenDB(connector)
		defer independent.Close()
		if _, err := independent.ExecContext(p.Ctx, `CREATE ROLE eo_brin_reader`); err != nil {
			t.Fatal(err)
		}
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
				defer cleanup(conn)
				check(t, conn, destination.recorder, true)
				if again := extensionDump(t, p.Ctx, destination.control); again != saved {
					t.Fatal("BRIN membership changed on redump")
				}
				if destination.name == "same_driver" {
					var name string
					if !oldRows.Next() {
						t.Fatal("lost retained BRIN row", oldRows.Err())
					}
					if err := oldRows.Scan(&name); err != nil || name != "brin_minmax_opcinfo" {
						t.Fatal("retained BRIN declaration changed", name, err)
					}
					if oldRows.Next() || oldRows.Err() != nil {
						t.Fatal("retained BRIN row multiplicity", oldRows.Err())
					}
					if err := oldRows.Close(); err != nil {
						t.Fatal(err)
					}
				}
				r := ownershipRunWithRecorder(t, p, conn, destination.recorder)
				for _, signature := range ownershipBRINSignatures {
					r(fixture.Exec("ALTER EXTENSION unaccent DROP FUNCTION pg_catalog."+signature, 0))
				}
				check(t, conn, destination.recorder, false)
				r(fixture.Exec(`DROP EXTENSION unaccent`, 0))
				r(ownershipCount(`SELECT count(*) FROM pg_proc WHERE oid IN (3383,3384,3385,3386)`, 4))
			})
		}
	})
}
