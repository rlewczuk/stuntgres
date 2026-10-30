package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// REL_18_6 hstore--1.4.sql binds = to hstore_eq; ALTER OPERATOR SET
// SCHEMA moves the operator alone (alter.c AlterObjectNamespace_internal).
// CASE and NULLIF retain their analyzed equality identity (ruleutils.c).
// Expectations below are source-derived, not acquired from either backend.
func TestReferenceExtensionOwnershipComparisonControls(t *testing.T) {
	fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := fixture.OwnershipRun(t, p, b, c)
		for _, q := range []string{
			`CREATE SCHEMA eo_old`, `CREATE SCHEMA eo_new`,
			`CREATE EXTENSION hstore SCHEMA eo_old`,
			`CREATE TABLE eo_old.inputs(id integer,l eo_old.hstore,r eo_old.hstore)`,
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(`INSERT INTO eo_old.inputs VALUES (1,'a=>1','a=>1'),(2,'a=>1','a=>2'),(3,NULL,'a=>1'),(4,'a=>1',NULL)`, 4))
		run(fixture.Exec(`SET search_path = pg_catalog,eo_old`, 0))
		run(fixture.Exec(`CREATE VIEW eo_old.comparisons AS SELECT id,CASE l WHEN r THEN 7 ELSE 9 END AS chosen,NULLIF(l,r)::text AS different FROM eo_old.inputs`, 0))
		run(fixture.Exec(`RESET search_path`, 0))
		query := `SELECT id,chosen,different FROM eo_old.comparisons ORDER BY id`
		want := fixture.Select(query, []string{"id", "chosen", "different"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(7), nil}, {int64(2), int64(9), `"a"=>"1"`}, {int64(3), int64(9), nil}, {int64(4), int64(9), `"a"=>"1"`}})
		statement, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		operatorID := fixture.OwnershipID(t, p, c, `SELECT 'eo_old.=(eo_old.hstore,eo_old.hstore)'::regoperator::bigint`)
		calleeID := fixture.OwnershipID(t, p, c, `SELECT 'eo_old.hstore_eq(eo_old.hstore,eo_old.hstore)'::regprocedure::bigint`)
		declaration := fixture.OwnershipSurvivorDeclaration(`eo_old.hstore_eq(eo_old.hstore,eo_old.hstore)`)
		original := fixture.OwnershipSnapshot(t, p, c, declaration)
		run(fixture.Exec(`ALTER OPERATOR eo_old.=(eo_old.hstore,eo_old.hstore) SET SCHEMA eo_new`, 0))
		if got := fixture.OwnershipID(t, p, c, `SELECT 'eo_new.=(eo_old.hstore,eo_old.hstore)'::regoperator::bigint`); got != operatorID {
			t.Fatal("movement replaced operator identity", got, operatorID)
		}
		if got := fixture.OwnershipID(t, p, c, `SELECT 'eo_old.hstore_eq(eo_old.hstore,eo_old.hstore)'::regprocedure::bigint`); got != calleeID {
			t.Fatal("movement replaced callee identity", got, calleeID)
		}
		fixture.OwnershipUnchanged(t, p, c, declaration, original)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		check := func(conn *sql.Conn, recorder *fixture.Recorder) {
			destination := b
			destination.Recorder = recorder
			r := fixture.OwnershipRun(t, p, destination, conn)
			r(want)
			r(fixture.Prepared(want))
			// This is the exact old admission failure: no comparison procedure
			// (indeed no procedure at all) can authenticate eo_new by accident.
			r(fixture.OwnershipCount(`SELECT count(*) FROM pg_proc WHERE pronamespace='eo_new'::regnamespace`, 0))
			r(fixture.Select(`SELECT n.nspname,pn.nspname,o.oprcode='eo_old.hstore_eq(eo_old.hstore,eo_old.hstore)'::regprocedure AS exact_callee,o.oprleft='eo_old.hstore'::regtype AS exact_left,o.oprright='eo_old.hstore'::regtype AS exact_right,o.oprresult='boolean'::regtype AS exact_result,o.oprowner=p.proowner AS same_owner,p.proisstrict,p.provolatile,p.proparallel,p.prosrc,p.probin,p.proacl IS NULL AS default_acl FROM pg_operator o JOIN pg_namespace n ON n.oid=o.oprnamespace JOIN pg_proc p ON p.oid=o.oprcode JOIN pg_namespace pn ON pn.oid=p.pronamespace WHERE o.oid='eo_new.=(eo_old.hstore,eo_old.hstore)'::regoperator`, []string{"nspname", "nspname", "exact_callee", "exact_left", "exact_right", "exact_result", "same_owner", "proisstrict", "provolatile", "proparallel", "prosrc", "probin", "default_acl"}, []string{"NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT", "BOOL"}, [][]any{{"eo_new", "eo_old", true, true, true, true, true, true, "i", "s", "hstore_eq", "$libdir/hstore", true}}))
			for _, dependency := range []struct{ class, object, referenceClass, reference string }{
				{"pg_operator", "'eo_new.=(eo_old.hstore,eo_old.hstore)'::regoperator", "pg_proc", "'eo_old.hstore_eq(eo_old.hstore,eo_old.hstore)'::regprocedure"},
				{"pg_operator", "'eo_new.=(eo_old.hstore,eo_old.hstore)'::regoperator", "pg_namespace", "'eo_new'::regnamespace"},
				{"pg_proc", "'eo_old.hstore_eq(eo_old.hstore,eo_old.hstore)'::regprocedure", "pg_namespace", "'eo_old'::regnamespace"},
			} {
				r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='`+dependency.class+`'::regclass AND objid=`+dependency.object+` AND refclassid='`+dependency.referenceClass+`'::regclass AND refobjid=`+dependency.reference+` AND deptype='n'`, 1))
			}
			for _, object := range []struct{ class, identity string }{{"pg_operator", "'eo_new.=(eo_old.hstore,eo_old.hstore)'::regoperator"}, {"pg_proc", "'eo_old.hstore_eq(eo_old.hstore,eo_old.hstore)'::regprocedure"}} {
				r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='`+object.class+`'::regclass AND objid=`+object.identity+` AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'`, 1))
			}
			// Fresh implicit binding uses the operator-only namespace, while
			// explicit calls still use the independently placed genuine callee.
			r(fixture.Exec(`SET search_path = pg_catalog,eo_new`, 0))
			for _, input := range []struct{ value, chosen, different any }{{"a=>1", int64(7), nil}, {"a=>2", int64(9), `"a"=>"2"`}, {nil, int64(9), nil}} {
				s := fixture.Select(`SELECT CASE $1::eo_old.hstore WHEN 'a=>1'::eo_old.hstore THEN 7 ELSE 9 END AS chosen,NULLIF($1::eo_old.hstore,'a=>1'::eo_old.hstore)::text AS different,eo_old.hstore_eq($1::eo_old.hstore,'a=>1'::eo_old.hstore) AS equal`, []string{"chosen", "different", "equal"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{input.chosen, input.different, nil}}, input.value)
				if input.value != nil {
					s.Rows[0][2] = input.value == "a=>1"
				}
				r(s)
				r(fixture.Prepared(s))
			}
			r(fixture.Exec(`RESET search_path`, 0))
		}
		check(c, b.Recorder)
		if b.PG {
			return
		}
		saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
		// Retained native rows remain immutable across whole-catalog replacement.
		oldRows, err := c.QueryContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer oldRows.Close()
		connector, err := stuntgres.NewDriver().OpenConnector("eo_comparison_operator_controls")
		if err != nil {
			t.Fatal(err)
		}
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		other := sql.OpenDB(connector)
		defer other.Close()
		for _, target := range []struct {
			control, recorded *sql.DB
			recorder          *fixture.Recorder
		}{{p.Native, b.DB, b.Recorder}, {other, recorded, recorder}} {
			if err := stuntgres.Reset(p.Ctx, target.control); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, target.control, strings.NewReader(saved)); err != nil {
				t.Fatal(err)
			}
			conn, err := target.recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			check(conn, target.recorder)
			fixture.OwnershipUnchanged(t, p, conn, declaration, original)
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		metadata, err := oldRows.ColumnTypes()
		if err != nil || len(metadata) != 3 || metadata[0].DatabaseTypeName() != "INT4" || metadata[1].DatabaseTypeName() != "INT4" || metadata[2].DatabaseTypeName() != "TEXT" {
			t.Fatal("retained comparison metadata", metadata, err)
		}
		for i, expected := range want.Rows {
			if !oldRows.Next() {
				t.Fatal("missing retained row", i, oldRows.Err())
			}
			var id, chosen int64
			var different any
			if err := oldRows.Scan(&id, &chosen, &different); err != nil {
				t.Fatal(err)
			}
			if _, ok := fixture.CompareRows([][]any{{id, chosen, fixture.Normalize(different)}}, [][]any{expected}, true); !ok {
				t.Fatal("retained comparison row changed", i, id, chosen, different)
			}
		}
		if oldRows.Next() || oldRows.Err() != nil {
			t.Fatal("retained comparison EOF", oldRows.Err())
		}
		if err := oldRows.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
