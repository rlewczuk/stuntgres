package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const triggerLongSource = "trigger_nullify_next_start_at_on_workspace_autostart_modification"
const triggerLongCanonical = "trigger_nullify_next_start_at_on_workspace_autostart_modificati"

func triggerNameError(q, state, message string) fixture.Step {
	s := fixture.ErrorDiagnostic(q, eventTriggerDiagnostic(state, message))
	s.ErrorStage = "execute"
	return s
}

func triggerNameScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step)), schemas ...string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		route := "direct"
		if prepared {
			route = "prepared"
		}
		t.Run(route, func(t *testing.T) {
			for _, schema := range schemas {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					records := new([]fixture.Diagnostic)
					_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, records)
					run := func(s fixture.Step) {
						*records = nil // Install the witness before Prepared performs Prepare.
						if prepared {
							s = fixture.Prepared(s)
						}
						if prepared && s.Query == "SELECT 1" {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, rec, s)
						var want []fixture.Diagnostic
						// Every overlong identifier token in these SQL strings is explicit.
						for _, token := range triggerNameTokens(s.Query) {
							want = append(want, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf("identifier %q will be truncated to %q", token[0], token[1])})
						}
						if len(*records) != len(want) {
							t.Fatalf("%s notices: got %#v want %#v", s.Query, *records, want)
						}
						for i := range want {
							if (*records)[i] != want[i] {
								t.Errorf("notice %d: got %#v want %#v", i, (*records)[i], want[i])
							}
						}
					}
					body(t, p, b, c, run)
				})
			}
		})
	}
}

func triggerNameTokens(q string) [][2]string {
	// This is an expectation table, not an implementation of identifier clipping.
	cases := [][2]string{
		{triggerLongSource, triggerLongCanonical},
		{triggerLongCanonical + "_other", triggerLongCanonical},
		{strings.Repeat("a", 64), strings.Repeat("a", 63)},
		{strings.Repeat("A", 64), strings.Repeat("A", 63)},
		{strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)},
		{strings.Repeat("é", 32), strings.Repeat("é", 31)},
	}
	var found [][2]string
	for _, pair := range cases {
		for range strings.Count(q, pair[0]) {
			found = append(found, pair)
		}
	}
	return found
}

func triggerNameSetup(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE tn_data(id integer PRIMARY KEY,v integer,mark text)", 0))
	run(fixture.Exec("CREATE TABLE tn_other(id integer PRIMARY KEY,v integer,mark text)", 0))
	run(fixture.Exec("CREATE FUNCTION tn_add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+TG_ARGV[0]::integer; NEW.mark:=TG_NAME; RETURN NEW; END$$", 0))
}

func triggerNameCreate(table, name, arg string) string {
	return "CREATE TRIGGER " + name + " BEFORE INSERT OR UPDATE OF v ON " + table + " FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION tn_add('" + arg + "')"
}

func triggerNameRows(q string, rows ...[]any) fixture.Step {
	return fixture.Select(q, []string{"id", "v", "mark"}, []string{"INT4", "INT4", "TEXT"}, rows)
}

func triggerNameCatalog(run func(fixture.Step), table, name string) {
	run(fixture.Select("SELECT tgname FROM pg_trigger WHERE tgrelid='"+table+"'::regclass AND NOT tgisinternal ORDER BY tgname", []string{"tgname"}, []string{"NAME"}, [][]any{{name}}))
	run(fixture.Select("SELECT DISTINCT trigger_name FROM information_schema.triggers WHERE event_object_table='"+table+"' ORDER BY trigger_name", []string{"trigger_name"}, []string{"NAME"}, [][]any{{name}}))
}

func triggerNameOID(t *testing.T, p *fixture.Pair, c *sql.Conn, table, name string) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_trigger WHERE tgrelid=$1::regclass AND tgname=$2", table, name).Scan(&oid); err != nil || oid <= 0 {
		t.Fatalf("trigger OID %d: %v", oid, err)
	}
	return oid
}

func TestReferenceTriggerDDLNameTruncationMinimal(t *testing.T) {
	triggerNameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{"BEGIN;", "CREATE TABLE r3_long_trigger(id integer);", "CREATE FUNCTION r3_long_trigger_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END;$$;", "CREATE TRIGGER trigger_nullify_next_start_at_on_workspace_autostart_modification AFTER UPDATE ON r3_long_trigger FOR EACH ROW EXECUTE FUNCTION r3_long_trigger_fn();", "ROLLBACK;"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='r3_long_trigger'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_proc WHERE proname='r3_long_trigger_fn'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_trigger WHERE tgname='"+triggerLongCanonical+"'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		triggerNameSetup(run)
		run(fixture.Exec(triggerNameCreate("tn_data", triggerLongSource, "3"), 0))
		triggerNameCatalog(run, "tn_data", triggerLongCanonical)
		triggerNameOID(t, p, c, "tn_data", triggerLongCanonical)
		run(triggerNameRows("INSERT INTO tn_data VALUES(1,5,NULL) RETURNING id,v,mark", []any{int64(1), int64(8), triggerLongCanonical}))
	})
}

func TestReferenceTriggerDDLNameTruncationBoundaries(t *testing.T) {
	cases := []struct{ name, source, want string }{
		{"63", strings.Repeat("a", 63), strings.Repeat("a", 63)},
		{"64", strings.Repeat("a", 64), strings.Repeat("a", 63)},
		{"quoted-case", strings.Repeat("A", 64), strings.Repeat("A", 63)},
		{"utf8-edge", strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)},
		{"utf8", strings.Repeat("é", 32), strings.Repeat("é", 31)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			triggerNameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				triggerNameSetup(run)
				name := `"` + tc.source + `"`
				if tc.name == "63" || tc.name == "64" {
					name = tc.source
				}
				run(fixture.Exec(triggerNameCreate("tn_data", name, "3"), 0))
				triggerNameCatalog(run, "tn_data", tc.want)
				run(triggerNameRows("INSERT INTO tn_data VALUES(1,5,NULL) RETURNING id,v,mark", []any{int64(1), int64(8), tc.want}))
			})
		})
	}
}

func triggerNameLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
	triggerNameSetup(run)
	run(fixture.Exec(triggerNameCreate("tn_data", triggerLongSource, "3"), 0))
	run(fixture.Exec(triggerNameCreate("tn_other", triggerLongSource, "11"), 0))
	oid := triggerNameOID(t, p, c, "tn_data", triggerLongCanonical)
	definition := func() {
		run(fixture.Select("SELECT t.tgtype,t.tgnargs,p.proname,t.tgqual IS NOT NULL AS has_when FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgrelid='tn_data'::regclass", []string{"tgtype", "tgnargs", "proname", "has_when"}, []string{"INT2", "INT2", "NAME", "BOOL"}, [][]any{{int64(23), int64(1), "tn_add", true}}))
	}
	definition()
	if oid == triggerNameOID(t, p, c, "tn_other", triggerLongCanonical) {
		t.Fatal("table-local triggers share OID")
	}
	q := "INSERT INTO tn_data VALUES($1,5,NULL) RETURNING id,v,mark"
	held := (eventDDLCase{t: t, p: p, b: b, c: c}).held(q)
	defer held.Statement.Close()
	fire := func(id, v int64, name any) {
		s := triggerNameRows(q, []any{id, v, name})
		s.Args = []any{id}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	fire(1, 8, triggerLongCanonical)
	run(triggerNameError(triggerNameCreate("tn_data", triggerLongCanonical+"_other", "7"), "42710", `trigger "`+triggerLongCanonical+`" for relation "tn_data" already exists`))
	fire(2, 8, triggerLongCanonical)
	if triggerNameOID(t, p, c, "tn_data", triggerLongCanonical) != oid {
		t.Fatal("duplicate changed OID")
	}
	definition()
	replace := strings.Replace(triggerNameCreate("tn_data", triggerLongCanonical+"_other", "7"), "CREATE TRIGGER", "CREATE OR REPLACE TRIGGER", 1)
	run(fixture.Exec(replace, 0))
	if triggerNameOID(t, p, c, "tn_data", triggerLongCanonical) != oid {
		t.Fatal("replace changed OID")
	}
	fire(3, 12, triggerLongCanonical)
	definition()
	run(triggerNameRows("INSERT INTO tn_data VALUES(31,-1,NULL) RETURNING id,v,mark", []any{int64(31), int64(-1), nil}))
	run(fixture.Exec("UPDATE tn_data SET mark='untouched' WHERE id=31", 1))
	run(triggerNameRows("SELECT id,v,mark FROM tn_data WHERE id=31", []any{int64(31), int64(-1), "untouched"}))
	run(triggerNameRows("INSERT INTO tn_data VALUES(30,5,NULL) RETURNING id,v,mark", []any{int64(30), int64(12), triggerLongCanonical}))
	run(fixture.Exec("ALTER TRIGGER "+triggerLongSource+" ON tn_data RENAME TO "+triggerLongCanonical+"_other", 0))
	run(fixture.Exec("ALTER TRIGGER "+triggerLongCanonical+" ON tn_data RENAME TO short", 0))
	fire(4, 12, "short")
	run(fixture.Exec(triggerNameCreate("tn_data", triggerLongSource, "3"), 0))
	run(triggerNameError("ALTER TRIGGER short ON tn_data RENAME TO "+triggerLongSource, "42710", `trigger "`+triggerLongCanonical+`" for relation "tn_data" already exists`))
	if triggerNameOID(t, p, c, "tn_data", "short") != oid {
		t.Fatal("failed rename changed OID")
	}
	// Both surviving definitions fire: canonical sorts after short.
	fire(5, 15, triggerLongCanonical)
	run(fixture.Exec("DROP TRIGGER "+triggerLongSource+" ON tn_data", 0))
	run(fixture.Exec("ALTER TRIGGER short ON tn_data RENAME TO "+triggerLongSource, 0))
	if triggerNameOID(t, p, c, "tn_data", triggerLongCanonical) != oid {
		t.Fatal("rename changed OID")
	}
	triggerNameCatalog(run, "tn_data", triggerLongCanonical)
	fire(6, 12, triggerLongCanonical)
	run(triggerNameError("DROP TRIGGER short ON tn_data", "42704", `trigger "short" for table "tn_data" does not exist`))
	run(triggerNameError("ALTER TRIGGER short ON tn_data RENAME TO absent", "42704", `trigger "short" for table "tn_data" does not exist`))
	run(fixture.Exec("DROP TRIGGER "+triggerLongCanonical+" ON tn_data", 0))
	fire(7, 5, nil)
	run(triggerNameRows("INSERT INTO tn_other VALUES(1,5,NULL) RETURNING id,v,mark", []any{int64(1), int64(16), triggerLongCanonical}))
}

func TestReferenceTriggerDDLNameTruncationLifecycle(t *testing.T) {
	triggerNameScenario(t, triggerNameLifecycle)
}

func TestReferenceTriggerDDLNameTruncationFeatureSmoke(t *testing.T) {
	t.Run("lifecycle", func(t *testing.T) { triggerNameScenario(t, triggerNameLifecycle) })
	t.Run("migration278", func(t *testing.T) { triggerNameScenario(t, triggerNameMigration278) })
}
