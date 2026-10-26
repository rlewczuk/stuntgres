package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var amcheckOverloads = []struct {
	name      string
	arguments int
}{
	{"bt_index_check", 1}, {"bt_index_check", 2}, {"bt_index_check", 3},
	{"bt_index_parent_check", 1}, {"bt_index_parent_check", 2}, {"bt_index_parent_check", 3}, {"bt_index_parent_check", 4},
}

func amcheckSelect(query string, null bool, args ...any) fixture.Step {
	var value any = ""
	if null {
		value = nil
	}
	return fixture.Select(query, []string{"ok"}, []string{"VOID"}, [][]any{{value}}, args...)
}
func amcheckSetup(t *testing.T, p *fixture.Pair) {
	t.Helper()
	for _, query := range []string{
		"CREATE EXTENSION amcheck",
		"CREATE TABLE am_heap(n INT,s TEXT)",
		"CREATE INDEX am_idx ON am_heap(n)",
		"CREATE INDEX am_desc ON am_heap(s DESC NULLS FIRST,n ASC NULLS LAST)",
		"CREATE INDEX am_expr ON am_heap(lower(s)) WHERE n>1",
		"CREATE INDEX am_partial ON am_heap(n) WHERE s='b'",
		"CREATE TABLE am_unique(n INT UNIQUE,s TEXT)",
		"CREATE UNIQUE INDEX am_unique_idx ON am_unique(n)",
	} {
		p.Step(t, fixture.Exec(query, 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO am_heap VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5))
	p.Step(t, fixture.Exec("INSERT INTO am_unique VALUES(1,'a'),(2,'b'),(NULL,NULL)", 3))
}

func TestReferenceAmcheckContract(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "am_shadow")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name+"/installation_rollback", func(t *testing.T) {
			tx, err := backend.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("CREATE EXTENSION amcheck", 0))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT count(*) AS installations FROM pg_extension WHERE extname='amcheck'", []string{"installations"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	}
	amcheckSetup(t, p)
	p.Step(t, fixture.Select(`SELECT p.proname,p.pronargs,p.proisstrict,p.provolatile,p.proparallel,p.proretset,p.prorettype::regtype::text AS result FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.proname IN ('bt_index_check','bt_index_parent_check') ORDER BY p.proname,p.pronargs`, []string{"proname", "pronargs", "proisstrict", "provolatile", "proparallel", "proretset", "result"}, []string{"NAME", "INT2", "BOOL", "CHAR", "CHAR", "BOOL", "TEXT"}, [][]any{
		{"bt_index_check", int64(1), true, "v", "r", false, "void"},
		{"bt_index_check", int64(2), true, "v", "r", false, "void"},
		{"bt_index_check", int64(3), true, "v", "r", false, "void"},
		{"bt_index_parent_check", int64(1), true, "v", "r", false, "void"},
		{"bt_index_parent_check", int64(2), true, "v", "r", false, "void"},
		{"bt_index_parent_check", int64(3), true, "v", "r", false, "void"},
		{"bt_index_parent_check", int64(4), true, "v", "r", false, "void"},
	}))
	for _, overload := range amcheckOverloads {
		arguments := []string{"'am_idx'::regclass"}
		for len(arguments) < overload.arguments {
			arguments = append(arguments, "true")
		}
		query := "SELECT " + overload.name + "(" + strings.Join(arguments, ",") + ") AS ok"
		p.Step(t, amcheckSelect(query, false))
		p.Step(t, fixture.Prepared(amcheckSelect(query, false)))
		labels := []string{"index", "heapallindexed", "checkunique"}
		if overload.name == "bt_index_parent_check" {
			labels = []string{"index", "heapallindexed", "rootdescend", "checkunique"}
		}
		named := make([]string, len(arguments))
		for i := range arguments {
			named[len(arguments)-i-1] = labels[i] + " => " + arguments[i]
		}
		p.Step(t, amcheckSelect("SELECT "+overload.name+"("+strings.Join(named, ",")+") AS ok", false))
		for nullable := range arguments {
			values := make([]any, len(arguments))
			parameters := make([]string, len(arguments))
			for i := range arguments {
				cast := "boolean"
				values[i] = true
				if i == 0 {
					cast, values[i] = "regclass", int64(17)
				}
				if i == nullable {
					values[i] = nil
				}
				parameters[i] = fmt.Sprintf("$%d::%s", i+1, cast)
			}
			p.Step(t, fixture.Prepared(amcheckSelect("SELECT "+overload.name+"("+strings.Join(parameters, ",")+") AS ok", true, values...)))
		}
	}
	p.Step(t, fixture.Select("SELECT bt_index_check('am_idx') IS NULL AS is_null", []string{"is_null"}, []string{"BOOL"}, [][]any{{false}}))
	p.Step(t, fixture.Select("SELECT bt_index_check(NULL::regclass) IS NULL AS is_null", []string{"is_null"}, []string{"BOOL"}, [][]any{{true}}))
	for _, backend := range p.Backends() {
		t.Run(backend.Name+"/held_parameters", func(t *testing.T) {
			statement, err := backend.DB.PrepareContext(p.Ctx, "SELECT bt_index_parent_check($1::regclass,$2,$3,$4) AS ok")
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: statement, SQL: "SELECT bt_index_parent_check($1::regclass,$2,$3,$4) AS ok"}
			for _, heap := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, amcheckSelect(held.SQL, false, "am_idx", heap, true, true))
			}
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, step) }
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'am_idx'::regclass::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec(fmt.Sprintf("CREATE SEQUENCE am_once START WITH %d", oid), 0))
			run(amcheckSelect("SELECT bt_index_check(nextval('am_once')::oid::regclass) AS ok", false))
			run(fixture.Select("SELECT last_value::int8 AS evaluated FROM am_once", []string{"evaluated"}, []string{"INT8"}, [][]any{{oid}}))
			run(fixture.Exec("CREATE SCHEMA am_shadow", 0))
			run(fixture.Exec("CREATE FUNCTION am_shadow.bt_index_check(regclass) RETURNS int LANGUAGE SQL AS $$ SELECT 42 $$", 0))
			run(fixture.Exec("SET search_path=am_shadow,public", 0))
			defer c.ExecContext(p.Ctx, "RESET search_path")
			run(fixture.Select("SELECT bt_index_check('am_heap') AS ordinary", []string{"ordinary"}, []string{"INT4"}, [][]any{{int64(42)}}))
			run(fixture.Exec("RESET search_path", 0))
		})
	}
}

func TestReferenceAmcheckHeapAndRootOptions(t *testing.T) {
	p := fixture.NewScenario(t)
	amcheckSetup(t, p)
	for _, index := range []string{"am_idx", "am_desc", "am_expr", "am_partial"} {
		for _, flags := range []string{"false,false", "true,false", "false,true", "true,true"} {
			p.Step(t, amcheckSelect("SELECT bt_index_parent_check('"+index+"',"+flags+") AS ok", false))
		}
	}
	p.Step(t, fixture.Exec("CREATE INDEX am_include ON am_heap(n) INCLUDE(s)", 0))
	p.Step(t, amcheckSelect("SELECT bt_index_parent_check('am_include',true,true,true) AS ok", false))
	p.Step(t, fixture.Exec("CREATE TABLE am_empty(n INT)", 0))
	p.Step(t, fixture.Exec("CREATE INDEX am_empty_idx ON am_empty(n DESC NULLS FIRST)", 0))
	p.Step(t, amcheckSelect("SELECT bt_index_parent_check('am_empty_idx',true,true,true) AS ok", false))
	p.Step(t, fixture.Exec("INSERT INTO am_empty VALUES(NULL),(NULL)", 2))
	p.Step(t, amcheckSelect("SELECT bt_index_parent_check('am_empty_idx',true,true,true) AS ok", false))
	for _, backend := range p.Backends() {
		t.Run(backend.Name+"/private", func(t *testing.T) {
			tx, err := backend.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, tx, backend.Recorder, step) }
			run(fixture.Exec("INSERT INTO am_heap VALUES(4,'D')", 1))
			run(fixture.Exec("SAVEPOINT own", 0))
			run(fixture.Exec("UPDATE am_heap SET n=5,s='E' WHERE n=4", 1))
			for _, index := range []string{"am_idx", "am_desc", "am_expr", "am_partial"} {
				run(amcheckSelect("SELECT bt_index_parent_check('"+index+"',true,true,true) AS ok", false))
			}
			run(fixture.Exec("DELETE FROM am_heap WHERE n=5", 1))
			run(amcheckSelect("SELECT bt_index_parent_check('am_idx',true,true,true) AS ok", false))
			run(fixture.Exec("ROLLBACK TO own", 0))
			run(amcheckSelect("SELECT bt_index_parent_check('am_expr',true,true,true) AS ok", false))
			// The admission of these private versions predates both new indexes.
			// The partial build also produces a genuine nonmember for n=4.
			run(fixture.Exec("CREATE INDEX am_private_ddl ON am_heap(lower(s)) WHERE n>4", 0))
			run(fixture.Exec("CREATE INDEX am_private_all ON am_heap(n)", 0))
			for _, index := range []string{"am_private_ddl", "am_private_all"} {
				run(amcheckSelect("SELECT bt_index_parent_check('"+index+"',true,true,true) AS ok", false))
			}
			run(fixture.Exec("UPDATE am_heap SET n=6,s='F' WHERE n=4", 1))
			run(amcheckSelect("SELECT bt_index_parent_check('am_private_ddl',true,true,true) AS ok", false))
			run(amcheckSelect("SELECT bt_index_parent_check('am_private_all',true,true,true) AS ok", false))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceAmcheckUniqueOptions(t *testing.T) {
	p := fixture.NewScenario(t)
	amcheckSetup(t, p)
	p.Step(t, fixture.Exec("CREATE UNIQUE INDEX am_nnd ON am_unique(s) NULLS NOT DISTINCT", 0))
	for _, index := range []string{"am_unique_idx", "am_unique_n_key", "am_nnd", "am_idx"} {
		p.Step(t, amcheckSelect("SELECT bt_index_check('"+index+"',true,true) AS ok", false))
		p.Step(t, fixture.Prepared(amcheckSelect("SELECT bt_index_parent_check('"+index+"',true,true,true) AS ok", false)))
	}
	p.Step(t, fixture.Exec("INSERT INTO am_unique VALUES(3,'c')", 1))
	p.Step(t, fixture.Exec("UPDATE am_unique SET n=4 WHERE n=3", 1))
	p.Step(t, amcheckSelect("SELECT bt_index_parent_check('am_unique_idx',true,true,true) AS ok", false))
	p.Step(t, fixture.Exec("DELETE FROM am_unique WHERE n=4", 1))
	p.Step(t, amcheckSelect("SELECT bt_index_parent_check('am_unique_idx',true,true,true) AS ok", false))
}
