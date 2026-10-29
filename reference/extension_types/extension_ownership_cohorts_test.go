package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func ownershipPrerequisiteCohorts(t *testing.T) {
	for _, s := range []string{"eo_cube", "eo_earth", "eo_new"} {
		fixture.SchemaReferenceCleanup(t, s)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{`CREATE SCHEMA eo_cube`, `CREATE SCHEMA eo_earth`, `CREATE SCHEMA eo_new`, `CREATE EXTENSION cube SCHEMA eo_cube`, `CREATE EXTENSION earthdistance SCHEMA eo_earth`, `ALTER TYPE eo_cube.cube RENAME TO original_cube`, `ALTER EXTENSION earthdistance DROP DOMAIN eo_earth.earth`} {
				run(fixture.Exec(q, 0))
			}
			// Only setup is catalog-driven: the stock upstream and native scripts have
			// different released callable subsets. Identity/behavior expectations below
			// are independent and never learned from these rows.
			detach := func(provider, kind, query string) {
				rows, err := c.QueryContext(p.Ctx, query, provider)
				if err != nil {
					t.Fatal(err)
				}
				var names []string
				for rows.Next() {
					var name string
					if err := rows.Scan(&name); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					names = append(names, name)
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				for _, name := range names {
					run(fixture.Exec("ALTER EXTENSION "+provider+" DROP "+kind+" "+name, 0))
				}
			}
			functions := `SELECT p.oid::regprocedure::text FROM pg_proc p JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname=$1 ORDER BY p.oid`
			detach("earthdistance", "FUNCTION", functions)
			detach("earthdistance", "OPERATOR", `SELECT o.oid::regoperator::text FROM pg_operator o JOIN pg_depend d ON d.classid='pg_operator'::regclass AND d.objid=o.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname=$1 ORDER BY o.oid`)
			run(fixture.Exec(`DROP EXTENSION earthdistance`, 0))
			run(fixture.Exec(`ALTER EXTENSION cube DROP TYPE eo_cube.original_cube`, 0))
			detach("cube", "FUNCTION", functions)
			run(fixture.Exec(`DROP EXTENSION cube`, 0))
			run(fixture.Exec(`CREATE EXTENSION cube SCHEMA eo_new`, 0))
			var original, current int64
			if err := c.QueryRowContext(p.Ctx, `SELECT 'eo_cube.original_cube'::regtype::bigint,'eo_new.cube'::regtype::bigint`).Scan(&original, &current); err != nil {
				t.Fatal(err)
			}
			if original == current {
				t.Fatal("distinct genuine installations reused a nominal type")
			}
			check := func(conn *sql.Conn, recorder *fixture.Recorder) {
				r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
				r(fixture.Select(`SELECT n.nspname,t.typname,bn.nspname AS base_schema,bt.typname AS base_name,t.typbasetype='eo_cube.original_cube'::regtype AS old_identity,t.typbasetype='eo_new.cube'::regtype AS new_identity FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_type bt ON bt.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=bt.typnamespace WHERE t.oid='eo_earth.earth'::regtype`, []string{"nspname", "typname", "base_schema", "base_name", "old_identity", "new_identity"}, []string{"NAME", "NAME", "NAME", "NAME", "BOOL", "BOOL"}, [][]any{{"eo_earth", "earth", "eo_cube", "original_cube", true, false}}))
				r(fixture.Select(`SELECT e.extname,n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname IN ('cube','earthdistance') ORDER BY e.extname`, []string{"extname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"cube", "eo_new"}}))
				r(fixture.Select(`SELECT count(*) FROM pg_depend WHERE classid='pg_type'::regclass AND objid='eo_earth.earth'::regtype AND refclassid='pg_type'::regclass AND refobjid='eo_cube.original_cube'::regtype AND deptype='n'`, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
				r(fixture.Select(`SELECT count(*) FROM pg_depend WHERE refclassid='pg_extension'::regclass AND deptype='e' AND objid IN ('eo_cube.original_cube'::regtype,'eo_earth.earth'::regtype) AND classid='pg_type'::regclass`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				r(fixture.Select(`SELECT count(*) FROM pg_type p JOIN pg_type a ON a.oid=p.typarray AND a.typelem=p.oid WHERE p.oid IN ('eo_cube.original_cube'::regtype,'eo_new.cube'::regtype,'eo_earth.earth'::regtype) AND p.typnamespace=a.typnamespace AND p.typowner=a.typowner`, []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
				r(fixture.Select(`SELECT count(*) FROM pg_operator WHERE oprnamespace='eo_cube'::regnamespace`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				s := fixture.Select(`SELECT eo_earth.latitude(eo_earth.ll_to_earth(0,0)) AS latitude,eo_earth.latitude(NULL::eo_earth.earth) AS absent,'(1,2)'::eo_cube.original_cube::text AS original,'(3,4)'::eo_new.cube::text AS current`, []string{"latitude", "absent", "original", "current"}, []string{"FLOAT8", "FLOAT8", "TEXT", "TEXT"}, [][]any{{float64(0), nil, "(1, 2)", "(3, 4)"}})
				r(s)
				r(fixture.Prepared(s))
				r(fixture.Select(`SELECT count(*) FROM pg_namespace WHERE nspname NOT IN ('public','eo_cube','eo_earth','eo_new','information_schema') AND nspname NOT LIKE 'pg_%'`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			check(c, b.Recorder)
			if b.PG {
				return
			}
			var output bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &output); err != nil {
				t.Fatal(err)
			}
			saved := output.String()
			connector, err := stuntgres.NewDriver().OpenConnector("eo_prerequisite_cohorts")
			if err != nil {
				t.Fatal(err)
			}
			recorded, recorder := fixture.RecordedNativePool(t, connector)
			other := sql.OpenDB(connector)
			defer other.Close()
			for _, destination := range []struct {
				control, recorded *sql.DB
				recorder          *fixture.Recorder
			}{
				{p.Native, b.DB, b.Recorder},
				{other, recorded, recorder},
			} {
				db := destination.control
				if err := stuntgres.Reset(p.Ctx, db); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, db, strings.NewReader(saved)); err != nil {
					t.Fatal(err)
				}
				conn, err := destination.recorded.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				check(conn, destination.recorder)
				conn.Close()
			}
		})
	}
}
