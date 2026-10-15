package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func sqljsonControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		`CREATE DOMAIN sqj_control_text AS text`,
		`CREATE DOMAIN sqj_control_json AS json`,
		`CREATE TYPE sqj_control_pair AS (n int,label text)`,
		`CREATE TABLE sqj_control(id int,doc sqj_control_text,j sqj_control_json DEFAULT JSON_OBJECT('default':1 WITH UNIQUE KEYS) CHECK (j::json IS JSON OBJECT WITH UNIQUE KEYS))`,
		`INSERT INTO sqj_control(id,doc) VALUES(1,'{"n":1}'),(2,NULL)`,
		`CREATE VIEW sqj_control_view AS SELECT id,JSON_OBJECT('doc':doc FORMAT JSON ABSENT ON NULL WITH UNIQUE KEYS RETURNING sqj_control_json) AS o,JSON_ARRAY(SELECT x.id FROM sqj_control x WHERE x.id<=s.id ORDER BY x.id RETURNING text) AS a,JSON_SCALAR(ROW(id,doc)::sqj_control_pair) AS r FROM sqj_control s`,
	} {
		affected := int64(0)
		if strings.HasPrefix(q, "INSERT") {
			affected = 2
		}
		run(fixture.Exec(q, affected))
	}
	check := fixture.Select(`SELECT id,o,a,r FROM sqj_control_view ORDER BY id`, []string{"id", "o", "a", "r"}, []string{"INT4", "JSON", "TEXT", "JSON"}, [][]any{
		{int64(1), `{"doc" : {"n":1}}`, `[1]`, `{"n":1,"label":"{\"n\":1}"}`},
		{int64(2), `{}`, `[1, 2]`, `{"n":2,"label":null}`},
	})
	sqljsonBoth(t, p, b, c, check)
	if b.PG {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, clause := range []string{"JSON_OBJECT(", "WITH UNIQUE KEYS", "ABSENT ON NULL", "JSON_ARRAY(", "JSON_SCALAR(", "IS JSON"} {
		if !strings.Contains(strings.ToUpper(dump.String()), clause) {
			t.Fatalf("dump lost symbolic clause %q", clause)
		}
	}
	old, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	descriptors, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	run(fixture.Exec(`CREATE TABLE sqj_control_sentinel(v int)`, 0))
	run(fixture.Exec(`INSERT INTO sqj_control_sentinel VALUES(99)`, 1))
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	for i, ct := range descriptors {
		if ct.DatabaseTypeName() != check.Types[i] {
			t.Fatalf("retained descriptor %d: %s", i, ct.DatabaseTypeName())
		}
	}
	if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	sqljsonBoth(t, p, b, c, check)
	var redump bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatalf("nondeterministic redump\n%s\n%s", dump.String(), redump.String())
	}
	if p.Postgres != nil {
		if _, err = p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
		if _, err = p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted SQL replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
		if _, err = p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
	}
	unchanged := func() {
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatal("failed Restore replaced sentinel catalog")
		}
		run(check)
	}
	for _, tc := range []struct{ expr, code, message string }{
		{`JSON('x'::text)`, "22P02", "invalid input syntax for type json"},
		{`JSON_OBJECT('a':1,'a':2 WITH UNIQUE KEYS)`, "22030", `duplicate JSON object key value: "a"`},
	} {
		script := `CREATE TABLE sqj_control_bad(v json); INSERT INTO sqj_control_bad VALUES(JSON_ARRAY(1)); INSERT INTO sqj_control_bad VALUES(` + tc.expr + `);`
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
		d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
		if tc.code == "22P02" {
			d.Detail = `Token "x" is invalid.`
			d.Context = `JSON data, line 1: x`
		}
		fixture.AssertDiagnostic(t, err, &d)
		unchanged()
	}
	for _, expr := range []string{
		`JSON_ARRAY($1::text)`, `JSON_ARRAY($1::text FORMAT JSON)`,
		`JSON_OBJECT(($1::text) VALUE 1)`, `JSON_OBJECT('a':$1::text FORMAT JSON)`,
		`JSON($1::text WITH UNIQUE KEYS)`, `JSON_SCALAR($1::text)`,
		`JSON_SERIALIZE($1::text)`, `($1::text IS JSON WITH UNIQUE KEYS)`,
		`JSON_ARRAY(SELECT $1::int)`, `JSON_ARRAY(SELECT id FROM sqj_control WHERE id=$1::int)`,
	} {
		script := `/*é😀*/ CREATE TABLE sqj_control_bad(v text); INSERT INTO sqj_control_bad VALUES(` + expr + `);`
		d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
		d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		unchanged()
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	rows, err := c.QueryContext(ctx, `SELECT JSON_ARRAY(SELECT id FROM sqj_control)`)
	if rows != nil || !errors.Is(err, context.Canceled) {
		if rows != nil {
			rows.Close()
		}
		t.Fatalf("precanceled query rows=%v error=%v", rows, err)
	}
	run(check)
}

func TestReferenceSQLJSONControls(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) { sqljsonControls(t, p, b, c) })
}

func TestReferenceSQLJSONDomainPredicate(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN sqj_control_json AS json`, 0))
		for _, expr := range []string{
			`'{}'::sqj_control_json IS JSON`,
			`NULL::sqj_control_json IS JSON OBJECT WITH UNIQUE KEYS`,
			`'{}'::sqj_control_json IS NOT JSON`,
		} {
			t.Run(expr, func(t *testing.T) {
				sqljsonDiagnostic(t, p, b, c, "SELECT "+expr+" AS v",
					fixture.ControlsReferenceDiagnostic("42804", "cannot use type sqj_control_json in IS JSON predicate"), true)
			})
		}
	})
}
