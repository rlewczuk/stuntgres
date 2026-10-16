package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonpathNavigateRawCheck(t *testing.T, p *fixture.Pair, db *sql.DB, s fixture.Step) {
	t.Helper()
	if !s.SelectRows {
		result, err := db.ExecContext(p.Ctx, s.Query, s.Args...)
		if err != nil {
			t.Fatal(err)
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != s.Affected {
			t.Fatalf("affected=%d want=%d err=%v", affected, s.Affected, err)
		}
		return
	}
	rows, err := db.QueryContext(p.Ctx, s.Query, s.Args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if s.CheckMetadata != nil {
		s.CheckMetadata(t, rows)
	}
	fixture.StringCoreRetainedRows(t, rows, nil, s.Columns, s.Types, s.Rows, false)
}

// One native-control phase with real emitted SQL replay, shared by the bounded
// smoke and full controls scenario. The PG backend itself has no control API.
func jsonpathNavigateReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step, future ...fixture.Step) []byte {
	t.Helper()
	if b.PG {
		return nil
	}
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
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE jpn_replacement(v int)`, 0))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if check.CheckMetadata != nil {
		check.CheckMetadata(t, old)
	}
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	if len(meta) != len(check.Types) {
		t.Fatal(meta)
	}
	for i, ct := range meta {
		if ct.DatabaseTypeName() != check.Types[i] {
			t.Fatal(ct)
		}
	}
	jsonpathNavigateBoth(t, p, b, c, check)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatalf("nondeterministic redump\nbefore:\n%s\nafter:\n%s", dump.String(), redump.String())
	}
	connector, err := stuntgres.NewDriver().OpenConnector("reference/" + t.Name() + "/fresh")
	if err != nil {
		t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	jsonpathNavigateRawCheck(t, p, fresh, check)
	for _, s := range future {
		jsonpathNavigateRawCheck(t, p, fresh, s)
	}
	// The check and replay both retain the actual recipe, not only materialized rows.
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted SQL replay", err)
		}
		pgCheck := check
		if check.CheckMetadata != nil {
			pgCheck = jsonpathNavigateViewMetadataStep(check, true)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, pgCheck)
		for _, s := range future {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
	}
	return bytes.Clone(dump.Bytes())
}

func TestReferenceJSONPathNavigateControls(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonpathNavigateStored(t, p, b, c)
		future := []fixture.Step{
			fixture.Exec(`INSERT INTO jpn_stored(id,doc) VALUES(3,'{"a":[3,4]}')`, 1),
			fixture.Select(`SELECT fallback,stored,virtual FROM jpn_stored WHERE id=3`, []string{"fallback", "stored", "virtual"}, []string{"JSONB", "JSONB", "JSONB"}, [][]any{{`[1, 2]`, `[3, 4]`, `[3, 4]`}}),
			jsonpathNavigateSelect(`SELECT changed AS changed FROM jpn_view WHERE id=3`, `[3, 4]`),
			fixture.Exec(`UPDATE jpn_stored SET path='$.a[1]' WHERE id=3`, 1),
			fixture.Select(`SELECT id FROM jpn_stored WHERE jsonb_path_query_array(doc,path)='[4]'::jsonb`, []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}),
		}
		dump := jsonpathNavigateReplay(t, p, b, c, check, future...)
		if !b.PG {
			unchanged := func() {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(after.Bytes(), dump) {
					t.Fatalf("failed Restore replaced persistent state\n%s", after.String())
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
			}
			script := `CREATE TABLE jpn_bad(v jsonb); INSERT INTO jpn_bad VALUES(jsonb_path_query_array('[1,2]','$[*]')); INSERT INTO jpn_bad VALUES(jsonb_path_query_array('{}','strict $.x'));`
			d := fixture.ControlsReferenceDiagnostic("2203A", `JSON object does not contain key "x"`)
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
			unchanged()
			for slot := range 4 {
				args := []string{`'[1,2]'`, `'$[*]'`, `'{}'`, `false`}
				args[slot] = "$1"
				script := `/*é*/ CREATE TABLE jpn_bad(v jsonb); INSERT INTO jpn_bad VALUES(jsonb_path_query_array(` + strings.Join(args, ",") + `));`
				d := fixture.ControlsReferenceDiagnostic("42P02", `parameters are not allowed in restore`)
				d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
				unchanged()
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			fixture.LexicalOrdinary(t, stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)), context.Canceled)
			unchanged()
			success := `CREATE TABLE jpn_values(v jsonb); INSERT INTO jpn_values VALUES(jsonb_path_query_array(path=>'$[*]',target=>'[1,2]'));`
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(success)); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, jsonpathNavigateSelect(`SELECT v AS changed FROM jpn_values`, `[1, 2]`))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
		}
		for _, s := range future {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
