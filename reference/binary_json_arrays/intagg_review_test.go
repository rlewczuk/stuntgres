package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIntaggValuesDiagnostics(t *testing.T) {
	intaggCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		intaggInstall(run)
		run(fixture.Exec("CREATE TABLE ia_values(a integer[])", 0))
		for _, tc := range []struct {
			query, code, message, position, hint string
		}{
			{"INSERT INTO ia_values VALUES(ia_addon.int_array_aggregate(1))", "42803", "aggregate functions are not allowed in VALUES", "30", ""},
			{"INSERT INTO ia_values VALUES(ia_addon.int_array_aggregate('bad'))", "22P02", `invalid input syntax for type integer: "bad"`, "59", ""},
			{"INSERT INTO ia_values VALUES(ia_addon.int_array_aggregate(1::bigint))", "42883", "function ia_addon.int_array_aggregate(bigint) does not exist", "30", "No function matches the given name and argument types. You might need to add explicit type casts."},
			{"INSERT INTO ia_values VALUES(absent.int_array_aggregate(missing))", "42703", `column "missing" does not exist`, "57", ""},
			{"INSERT INTO ia_values VALUES(ia_addon.int_array_aggregate(1) FILTER(WHERE missing))", "42703", `column "missing" does not exist`, "75", ""},
		} {
			t.Run(tc.query, func(t *testing.T) {
				intaggError(t, p, b, c, tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint}, false)
			})
		}
		run(fixture.Select("SELECT count(*) FROM ia_values", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceIntaggTemporaryControls(t *testing.T) {
	intaggCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ia_retention_baseline(v integer)", 0))
		run(fixture.Exec("INSERT INTO ia_retention_baseline VALUES(17)", 1))
		var baseline string
		if !b.PG {
			baseline = intaggDump(t, p.Ctx, p.Native)
		}
		run(fixture.Exec("CREATE TEMP TABLE ia_retention_seed(id integer,v integer,a integer[])", 0))
		run(fixture.Exec("INSERT INTO ia_retention_seed VALUES(1,2,'{2,2,NULL}'),(2,NULL,'{}'),(3,2,NULL)", 3))
		var namespace string
		if err := c.QueryRowContext(p.Ctx, "SELECT schemaname FROM pg_tables WHERE tablename='ia_retention_seed'").Scan(&namespace); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(namespace, "pg_temp_") {
			t.Fatal("physical TEMP namespace", namespace)
		}
		qualified := `"` + strings.ReplaceAll(namespace, `"`, `""`) + `"`
		installer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer installer.Close()
		fixture.RunStep(t, p.Ctx, installer, b.Recorder, fixture.Exec("CREATE EXTENSION intagg SCHEMA "+qualified+" VERSION '1.1'", 0))
		run(fixture.Exec("CREATE TEMP VIEW ia_retention_aggregate AS SELECT "+qualified+".int_array_aggregate(v ORDER BY id) AS a FROM ia_retention_seed", 0))
		run(fixture.Exec("CREATE TEMP VIEW ia_retention_enum AS SELECT u.v,u.n FROM ia_retention_seed,"+qualified+".int_array_enum(a) WITH ORDINALITY AS u(v,n)", 0))
		run(fixture.Exec("CREATE TEMP VIEW ia_retention_target AS SELECT "+qualified+".int_array_enum(a) AS v FROM ia_retention_seed", 0))
		var aggregateOID, enumOID int64
		for name, target := range map[string]*int64{"int_array_aggregate": &aggregateOID, "int_array_enum": &enumOID} {
			if err := c.QueryRowContext(p.Ctx, "SELECT p.oid::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname=$2", namespace, name).Scan(target); err != nil {
				t.Fatal(err)
			}
		}
		if aggregateOID <= 0 || enumOID <= 0 || aggregateOID == enumOID {
			t.Fatal("distinct installed procedure identities", aggregateOID, enumOID)
		}
		checks := []fixture.Step{
			intaggArray(b, "SELECT a FROM ia_retention_aggregate", []string{"a"}, []string{"_INT4"}, [][]any{{"{2,NULL,2}"}}),
			fixture.Select("SELECT v,n FROM ia_retention_enum ORDER BY n", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}, {int64(2), int64(2)}, {nil, int64(3)}}),
			fixture.Select("SELECT DISTINCT p.proname FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND d.refclassid='pg_proc'::regclass::oid AND p.proname IN ('int_array_aggregate','int_array_enum') ORDER BY p.proname", []string{"proname"}, []string{"NAME"}, [][]any{{"int_array_aggregate"}, {"int_array_enum"}}, namespace),
			fixture.Select("SELECT p.proname,p.oid::bigint AS oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname IN ('int_array_aggregate','int_array_enum') ORDER BY p.proname", []string{"proname", "oid"}, []string{"NAME", "INT8"}, [][]any{{"int_array_aggregate", aggregateOID}, {"int_array_enum", enumOID}}, namespace),
		}
		target := fixture.Select("SELECT v FROM ia_retention_target", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {nil}})
		target.Bag = true
		check := func() {
			// Observe genuine procedure dependencies first after replacement.
			fixture.ArrayAggBoth(run, checks[2])
			for i, s := range checks {
				if i != 2 {
					fixture.ArrayAggBoth(run, s)
				}
			}
			fixture.ArrayAggBoth(run, target)
		}
		check()
		if b.PG {
			return
		} // PostgreSQL exercises real TEMP SQL, not native controls.
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		check()
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='ia_retention_baseline'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(baseline)); err != nil {
			t.Fatal(err)
		}
		check()
		run(fixture.Select("SELECT v FROM ia_retention_baseline", []string{"v"}, []string{"INT4"}, [][]any{{int64(17)}}))
	})
}
