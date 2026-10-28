package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Stored-column projections and COUNT carry known native nullability, unlike
// computed extraction expressions. pq reports nullability as unknown.
func jsonbExtractStoredMetadata(s fixture.Step, pg, nullable bool) fixture.Step {
	columns := make([]fixture.JSONTypeSourceColumn, len(s.Types))
	for i := range columns {
		columns[i] = fixture.JSONTypeSourceColumn{Index: i, Nullable: nullable}
	}
	return jsonEachWithMetadata(s, pg, columns...)
}

func jsonbExtractStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE TABLE jbex_store(id int,j jsonb DEFAULT '{"a":"default","n":[10,20,30]}',v text GENERATED ALWAYS AS (j->>'a') STORED,g jsonb GENERATED ALWAYS AS (jsonb_extract_path(j,'a')) STORED,d jsonb DEFAULT ('{"a":7}'::jsonb -> 'a'),dt text DEFAULT jsonb_extract_path_text('{"a":"d"}','a'),CONSTRAINT jbex_valid CHECK(j->>'a' <> 'bad'))`, 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE INDEX jbex_index ON jbex_store((j->>'a'))`, 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE INDEX jbex_predicate ON jbex_store(id) WHERE jsonb_extract_path_text(j,'a') <> 'bad'`, 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbex_store(id) VALUES(1)`, 1))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbex_store(id,j) VALUES(2,'{"a":"second","n":[40,50]}')`, 1))
	jsonTypeRun(t, p, b, c, fixture.Exec(`UPDATE jbex_store SET j='{"a":"changed","n":[60,70]}' WHERE id=2`, 1))
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE VIEW jbex_view AS SELECT id,j->'a' AS raw,j->>'a' AS txt,j#>'{n,-1}' AS tail,j#>>'{n,0}' AS first,jsonb_extract_path(j,'a') AS fn,jsonb_extract_path_text(j,VARIADIC '{a}'::text[]) AS vt,jsonb_extract_path(from_json=>j,VARIADIC path_elems=>'{a}'::text[]) AS named FROM jbex_store`, 0))
	check := fixture.Select(`SELECT id,raw,txt,tail,first,fn,vt,named FROM jbex_view ORDER BY id`, []string{"id", "raw", "txt", "tail", "first", "fn", "vt", "named"}, []string{"INT4", "JSONB", "TEXT", "JSONB", "TEXT", "JSONB", "TEXT", "JSONB"}, [][]any{{int64(1), `"default"`, "default", "30", "10", `"default"`, "default", `"default"`}, {int64(2), `"changed"`, "changed", "70", "60", `"changed"`, "changed", `"changed"`}})
	check = jsonbExtractStoredMetadata(check, b.PG, true)
	jsonTypeBoth(t, p, b, c, check)
	jsonTypeBoth(t, p, b, c, jsonbExtractStoredMetadata(fixture.Select(`SELECT v,g,d,dt FROM jbex_store WHERE j->>'a'='changed'`, []string{"v", "g", "d", "dt"}, []string{"TEXT", "JSONB", "JSONB", "TEXT"}, [][]any{{"changed", `"changed"`, "7", "d"}}), b.PG, true))
	return check
}

func jsonbExtractCheckError(q string) fixture.Step {
	d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jbex_store" violates check constraint "jbex_valid"`)
	d.Detail = `Failing row contains (4, {"a": "bad"}, bad, "bad", 7, d).`
	d.Schema, d.Table, d.Constraint = "public", "jbex_store", "jbex_valid"
	s := fixture.ErrorDiagnostic(q, d)
	s.ErrorStage = "execute"
	return s
}

func jsonbExtractStoredReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	check := jsonbExtractStored(t, p, b, c)
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE TABLE jbex_copy AS SELECT j->'a' AS raw,jsonb_extract_path_text(j,'a') AS txt FROM jbex_store`, 2))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbex_copy SELECT j#>'{a}',j#>>'{a}' FROM jbex_store WHERE id=1`, 1))
	jsonTypeBoth(t, p, b, c, jsonbExtractStoredMetadata(fixture.Select(`SELECT raw,txt FROM jbex_copy ORDER BY txt`, []string{"raw", "txt"}, []string{"JSONB", "TEXT"}, [][]any{{`"changed"`, "changed"}, {`"default"`, "default"}, {`"default"`, "default"}}), b.PG, true))
	jsonTypeBoth(t, p, b, c, jsonbExtractCheckError(`INSERT INTO jbex_store(id,j) VALUES(3,'{"a":"good"}'),(4,'{"a":"bad"}')`))
	jsonTypeBoth(t, p, b, c, check)
	jsonTypeBoth(t, p, b, c, jsonbExtractStoredMetadata(fixture.Select(`SELECT count(*) AS n FROM jbex_store`, []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}), b.PG, false))
}

func TestReferenceJSONBExtractStoredReplay(t *testing.T) {
	jsonbExtractScenario(t, jsonbExtractStoredReplay)
}

func jsonbExtractControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	if b.PG {
		return
	} // PostgreSQL has no Dump/Reset/Restore API; common SQL ran above.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
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
	jsonTypeRun(t, p, b, c, fixture.Exec(`CREATE TABLE jbex_replacement(v int)`, 0))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbex_replacement VALUES(99)`, 1))
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	for i, ct := range meta {
		if ct.DatabaseTypeName() != check.Types[i] {
			t.Fatal(ct)
		}
		if nullable, known := ct.Nullable(); !nullable || !known {
			t.Fatalf("retained column%d Nullable=%t/%t want=true/true", i, nullable, known)
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	unchanged := func() {
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatal("replacement changed dump")
		}
		jsonTypeBoth(t, p, b, c, check)
	}
	unchanged()
	script := `CREATE TABLE jbex_broken(v jsonb); INSERT INTO jbex_broken VALUES('{}'); INSERT INTO jbex_broken VALUES('oops'::jsonb -> 'a');`
	d := jsonbExtractInputError("", false).Diagnostic
	d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "'oops'")])) + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), d)
	unchanged()
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted dump replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, jsonEachWithMetadata(check, true))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`INSERT INTO jbex_store(id) VALUES(3)`, 1))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, jsonEachWithMetadata(fixture.Select(`SELECT v,g,d,dt FROM jbex_store WHERE id=3`, []string{"v", "g", "d", "dt"}, []string{"TEXT", "JSONB", "JSONB", "TEXT"}, [][]any{{"default", `"default"`, "7", "d"}}), true))
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceJSONBExtractNativeControls(t *testing.T) {
	jsonbExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbExtractStored(t, p, b, c)
		jsonbExtractControls(t, p, b, c, check)
	})
}
