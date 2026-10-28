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
		dump := fixture.JSONPathNavigateReplay(t, p, b, c, check, future...)
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
