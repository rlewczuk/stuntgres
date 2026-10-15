package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonExtractStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE TABLE jex_store(id int,j json DEFAULT '{"a":"default","n":[10,20,30]}',v text GENERATED ALWAYS AS (j->>'a') STORED,d json DEFAULT ('{"a":7}'::json -> 'a'),dt text DEFAULT json_extract_path_text('{"a":"d"}','a'),CHECK(j->>'a' <> 'bad'))`, 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE INDEX jex_index ON jex_store((j->>'a'))`, 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jex_store(id) VALUES(1)`, 1))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jex_store(id,j) VALUES(2,'{"a":"second","n":[40,50]}')`, 1))
	jsonTypeRun(t, p, b, c, fixture.Exec(`UPDATE jex_store SET j='{"a":"changed","n":[60,70]}' WHERE id=2`, 1))
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE VIEW jex_view AS SELECT id,j->'a' AS raw,j->>'a' AS txt,j#>'{n,-1}' AS tail,j#>>'{n,0}' AS first,json_extract_path(j,'a') AS fn,json_extract_path_text(j,VARIADIC '{a}'::text[]) AS vt,json_extract_path(from_json=>j,VARIADIC path_elems=>'{a}'::text[]) AS named FROM jex_store`, 0))
	check := fixture.Select(`SELECT id,raw,txt,tail,first,fn,vt,named FROM jex_view ORDER BY id`, []string{"id", "raw", "txt", "tail", "first", "fn", "vt", "named"}, []string{"INT4", "JSON", "TEXT", "JSON", "TEXT", "JSON", "TEXT", "JSON"}, [][]any{{int64(1), `"default"`, "default", "30", "10", `"default"`, "default", `"default"`}, {int64(2), `"changed"`, "changed", "70", "60", `"changed"`, "changed", `"changed"`}})
	jsonTypeBoth(t, p, b, c, check)
	jsonTypeBoth(t, p, b, c, fixture.Select(`SELECT v,d,dt FROM jex_store WHERE j->>'a'='changed'`, []string{"v", "d", "dt"}, []string{"TEXT", "JSON", "TEXT"}, [][]any{{"changed", "7", "d"}}))
	return check
}

func jsonExtractStoredReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	jsonExtractStored(t, p, b, c)
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE TABLE jex_copy AS SELECT j->'a' AS raw,json_extract_path_text(j,'a') AS txt FROM jex_store`, 2))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jex_copy SELECT j#>'{a}',j#>>'{a}' FROM jex_store WHERE id=1`, 1))
	jsonTypeBoth(t, p, b, c, fixture.Select(`SELECT raw,txt FROM jex_copy ORDER BY txt`, []string{"raw", "txt"}, []string{"JSON", "TEXT"}, [][]any{{`"changed"`, "changed"}, {`"default"`, "default"}, {`"default"`, "default"}}))
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE TABLE jex_bad(id int,j json)`, 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jex_bad VALUES(1,'{"a":"ok"}'),(2,'{"x":"\u0000"}')`, 2))
	q := `INSERT INTO jex_copy SELECT j->'a',j->>'a' FROM jex_bad ORDER BY id`
	s := jsonExtractUnicodeError(q, `{"x":"\u0000"}`)
	s.SelectRows = false
	jsonTypeBoth(t, p, b, c, s)
	jsonTypeBoth(t, p, b, c, fixture.Select(`SELECT count(*) AS n FROM jex_copy`, []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
}
func TestReferenceJSONExtractStoredReplay(t *testing.T) {
	jsonExtractScenario(t, jsonExtractStoredReplay)
}

func jsonExtractControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	if b.PG {
		return
	} // PostgreSQL has no native Dump/Reset/Restore API.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"json_extract_path", "json_extract_path_text", "->", "#>"} {
		if !strings.Contains(dump.String(), token) {
			t.Errorf("dump missing symbolic %q: %s", token, dump.String())
		}
	}
	old, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE TABLE jex_replacement(v int)`, 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jex_replacement VALUES(99)`, 1))
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	for i, ct := range meta {
		if ct.DatabaseTypeName() != check.Types[i] {
			t.Fatal(ct)
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	jsonTypeBoth(t, p, b, c, check)
	unchanged := func() {
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatal("failed replacement changed dump")
		}
		jsonTypeRun(t, p, b, c, check)
	}
	unchanged()
	script := `CREATE TABLE jex_broken(v text); INSERT INTO jex_broken VALUES('prefix'); INSERT INTO jex_broken VALUES('{"x":"\u0000"}'::json ->> 'x');`
	d := jsonExtractUnicodeError("", `{"x":"\u0000"}`).Diagnostic
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), d)
	unchanged()
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual dump replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`INSERT INTO jex_store(id) VALUES(3)`, 1))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select(`SELECT v FROM jex_store WHERE id=3`, []string{"v"}, []string{"TEXT"}, [][]any{{"default"}}))
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
	}
}
func TestReferenceJSONExtractNativeControls(t *testing.T) {
	jsonExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonExtractStored(t, p, b, c)
		jsonExtractControls(t, p, b, c, check)
	})
}
