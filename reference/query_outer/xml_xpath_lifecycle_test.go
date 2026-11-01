package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceXMLXPathLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "xpath_schema", "xpath_schema_new")
	resultsScenario(t, xpathLifecycle)
}
func xpathLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE xpath_life(id integer,p text,x xml,m text[],saved xml[])", 0),
		fixture.Exec(`INSERT INTO xpath_life VALUES(1,'/r','<r/>','{}',xpath('/r','<r/>'::xml)),(2,'/missing','<r/>','{}','{NULL}')`, 2),
		fixture.Exec("CREATE VIEW xpath_life_view AS SELECT id,xpath(p,x) AS a,xpath(p,x,m) AS b,xpath_exists(p,x) AS c,xpath_exists(p,x,m) AS d FROM xpath_life", 0),
		fixture.Exec(`CREATE VIEW xpath_literal_map_view AS SELECT xpath('/q:r','<r xmlns="urn:x"/>'::xml,ARRAY[ARRAY['q','urn:x']]) AS v`, 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	fixture.SQLJSONBoth(t, p, b, c, xpathStoredArrayStep("SELECT v FROM xpath_literal_map_view", b.PG, `{"<r xmlns=\"urn:x\"/>"}`))
	want := fixture.Select("SELECT id,a,b,c,d FROM xpath_life_view ORDER BY id", []string{"id", "a", "b", "c", "d"}, []string{"INT4", "_XML", "_XML", "BOOL", "BOOL"}, [][]any{{int64(1), "{<r/>}", "{<r/>}", true, true}, {int64(2), "{}", "{}", false, false}})
	fixture.SQLJSONBoth(t, p, b, c, want)
	var definition string
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('xpath_life_view'::regclass,true)").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW xpath_life_replay AS "+definition, 0))
	replay := want
	replay.Query = "SELECT id,a,b,c,d FROM xpath_life_replay ORDER BY id"
	fixture.SQLJSONBoth(t, p, b, c, replay)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE xpath_life RENAME TO xpath_life_renamed", 0))
	fixture.SQLJSONBoth(t, p, b, c, want)
	for _, q := range []string{"CREATE SCHEMA xpath_schema", "ALTER TABLE xpath_life_renamed SET SCHEMA xpath_schema", "ALTER SCHEMA xpath_schema RENAME TO xpath_schema_new"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.SQLJSONBoth(t, p, b, c, want)
	fixture.SQLJSONBoth(t, p, b, c, replay)
	held, err := c.PrepareContext(p.Ctx, want.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if b.PG {
		return
	}
	old, err := c.QueryContext(p.Ctx, "SELECT saved AS v FROM xpath_schema_new.xpath_life_renamed ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.SQLJSONBoth(t, p, b, c, want)
	fixture.SQLJSONBoth(t, p, b, c, replay)
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: want.Query}, b.Recorder, want)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatal("XPath replay changed deterministic dump")
	}
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("xpath-independent")
	if err != nil {
		t.Fatal(err)
	}
	destination := sql.OpenDB(connector)
	defer destination.Close()
	if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	defer recorded.Close()
	fixture.RunStep(t, p.Ctx, recorded, recorder, want)
	var independent bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, destination, &independent); err != nil {
		t.Fatal(err)
	}
	if independent.String() != dump.String() {
		t.Fatal("independent destination changed recipe")
	}
	// A staged valid row cannot escape a later failing XPath or recursive parameter.
	prefix := `CREATE TABLE staged_xpath(v xml[]); INSERT INTO staged_xpath VALUES(xpath('/r','<r/>'::xml)); `
	bad := prefix + `INSERT INTO staged_xpath VALUES(xpath('[','<r/>'::xml,'{}'::text[]));`
	d := xpathError("10608", "invalid XPath expression", "Invalid expression", 0)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
	for _, operand := range []string{`xpath($1,'<r/>'::xml)`, `xpath('/r',$1::xml)`, `xpath('/r','<r/>'::xml,ARRAY[ARRAY['q',$1]])`} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+"INSERT INTO staged_xpath VALUES("+operand+");")); err == nil {
			t.Fatal("recursive parameter admitted")
		}
	}
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
		t.Fatal(err)
	}
	if after.String() != dump.String() {
		t.Fatal("rejected restore changed catalog")
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE xpath_schema_new.xpath_life_renamed SET saved=xpath('/new','<new/>'::xml)", 2))
	fixture.SQLJSONBoth(t, p, b, c, xpathStoredArrayStep("SELECT saved AS v FROM xpath_schema_new.xpath_life_renamed ORDER BY id", false, "{<new/>}", "{<new/>}"))
	metadata := xpathStoredArrayStep("", false)
	metadata.CheckMetadata(t, old)
	for _, expected := range []string{"{<r/>}", "{NULL}"} {
		if !old.Next() {
			t.Fatal("retained cursor lost row", old.Err())
		}
		var value any
		if err := old.Scan(&value); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, false, "_XML", value)
		if got, ok := value.(string); !ok || got != expected {
			t.Fatal("retained XML[] changed", value, expected)
		}
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("retained cursor tail", old.Err())
	}
}

func TestReferenceXMLXPathCancellation(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xpath_cancel(v xml[])", 0))
		q := "INSERT INTO xpath_cancel SELECT xpath('/r','<r/>'::xml)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if !b.PG {
			canceled, stop := context.WithCancel(p.Ctx)
			stop()
			expired, finish := context.WithDeadline(p.Ctx, time.Unix(1, 0))
			defer finish()
			for _, ctx := range []context.Context{canceled, expired} {
				for _, executor := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}} {
					_, err := executor.ExecContext(ctx, q)
					if !errors.Is(err, ctx.Err()) {
						t.Fatal("command interruption", err, ctx.Err())
					}
					if _, ok := err.(interface{ SQLState() string }); ok {
						t.Fatal("ordinary context acquired SQLSTATE")
					}
				}
			}
			var before bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			reader := &attributesCancelRestoreReader{prefix: []byte(`CREATE TABLE xpath_staged(v xml[]); INSERT INTO xpath_staged VALUES(xpath('/r','<r/>'::xml));`), rest: strings.NewReader("SELECT 1;"), cancel: cancel}
			err := stuntgres.Restore(ctx, p.Native, reader)
			if !errors.Is(err, context.Canceled) || !reader.entered {
				t.Fatal("restore prefix cancellation", err, reader.entered)
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() {
				t.Fatal("canceled staging changed catalog")
			}
		}
		fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, "SELECT count(*) AS v FROM xpath_cancel", "INT8", int64(0)))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}, b.Recorder, fixture.Exec(q, 1))
		fixture.SQLJSONBoth(t, p, b, c, xpathStoredArrayStep("SELECT v FROM xpath_cancel", b.PG, "{<r/>}"))
	})
}
