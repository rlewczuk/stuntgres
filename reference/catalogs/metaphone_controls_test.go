package reference_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func metaphoneManifest(t *testing.T, dump, schema, owner string) {
	t.Helper()
	const prefix = "-- stuntgres-extension-owners-v1 "
	if strings.Count(dump, prefix) != 1 {
		t.Fatal("expected one owner manifest")
	}
	start := strings.Index(dump, prefix) + len(prefix)
	end := strings.IndexByte(dump[start:], '\n')
	if end < 0 {
		t.Fatal("unterminated owner manifest")
	}
	data, err := base64.StdEncoding.DecodeString(dump[start : start+end])
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Name    string `json:"name"`
		Schema  string `json:"schema"`
		Owner   string `json:"owner"`
		Version string `json:"version"`
		Members []struct {
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			Signature string `json:"signature"`
			Owner     string `json:"owner"`
		} `json:"members"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Name != "fuzzystrmatch" || record.Schema != schema || record.Owner != owner || record.Version != "1.2" || len(record.Members) != 8 {
		t.Fatal("owner manifest", record)
	}
	want := map[string]bool{
		"daitch_mokotoff:25": true, "levenshtein:25,25": true, "levenshtein:25,25,23,23,23": true,
		"levenshtein_less_equal:25,25,23": true, "levenshtein_less_equal:25,25,23,23,23,23": true,
		"metaphone:25,23": true, "dmetaphone:25": true, "dmetaphone_alt:25": true,
	}
	for _, member := range record.Members {
		key := member.Name + ":" + member.Signature
		if !want[key] || member.Kind != "procedure" || member.Owner != owner {
			t.Fatal("symbolic member", member)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatal("missing manifest members", want)
	}
}

func metaphoneControlMetadata(t *testing.T, rows *sql.Rows, check fixture.Step) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, check.Columns) {
		t.Fatal("retained columns", columns, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(check.Types) {
		t.Fatal("retained types", meta, err)
	}
	for i, typ := range meta {
		want := reflect.TypeFor[string]()
		if check.Types[i] == "INT4" {
			want = reflect.TypeFor[int64]()
		}
		if typ.DatabaseTypeName() != check.Types[i] || typ.ScanType() != want {
			t.Fatal("retained descriptor", i, typ)
		}
	}
}

func TestReferenceMetaphoneControls(t *testing.T) {
	metaphoneCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE USER phon_owner SUPERUSER", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION phon_owner", 0))
		check := metaphoneStored(t, p, b, c)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		metaphoneInventory(t, p, b, c, "phon_addon", "phon_owner", false)
		if b.PG {
			return
		} // Public controls have no PostgreSQL equivalent.
		dump := extensionDump(t, p.Ctx, p.Native)
		metaphoneManifest(t, dump, "phon_addon", "phon_owner")
		install, consumer := strings.Index(dump, "CREATE EXTENSION"), strings.Index(dump, "CREATE TABLE")
		if install < 0 || consumer < 0 || install > consumer {
			t.Fatal("provider must precede consumers", dump)
		}
		q := "SELECT phon_addon.metaphone($1,$2),phon_addon.dmetaphone($1),phon_addon.dmetaphone_alt($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		frame := fixture.Select(q, []string{"metaphone", "dmetaphone", "dmetaphone_alt"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"KM", "KMP", "KMP"}}, "gumbo", 4)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
		retained, err := c.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		metaphoneControlMetadata(t, retained, check)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='fuzzystrmatch'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(extensionMissing("SELECT metaphone('gumbo',4)", "metaphone", "unknown, integer"))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
		metaphoneControlMetadata(t, retained, check)
		levenshteinControlRows(t, retained, check)
		run(check)
		run(fixture.Prepared(check))
		run(metaphoneLiteralView())
		run(fixture.Prepared(metaphoneLiteralView()))
		metaphoneInventory(t, p, b, c, "phon_addon", "phon_owner", false)
		extensionControlsFailures(t, p, c, b.Recorder, dump, check)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
		ioFailure := errors.New("metaphone restore reader failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: ioFailure}); !errors.Is(err, ioFailure) {
			t.Fatal("reader failure", err)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &daitchRestoreCancelReader{cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("staged cancellation", err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
		run(check)
		run(metaphoneLiteralView())
		other := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "phon_destination")
		if _, err := other.ExecContext(p.Ctx, "CREATE USER phon_owner NOSUPERUSER"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		rows, err := other.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		metaphoneControlMetadata(t, rows, check)
		levenshteinControlRows(t, rows, check)
		rows, err = other.QueryContext(p.Ctx, q, "gumbo", 4)
		if err != nil {
			t.Fatal(err)
		}
		metaphoneControlMetadata(t, rows, frame)
		levenshteinControlRows(t, rows, frame)
		literals := metaphoneLiteralView()
		rows, err = other.QueryContext(p.Ctx, literals.Query)
		if err != nil {
			t.Fatal(err)
		}
		metaphoneControlMetadata(t, rows, literals)
		levenshteinControlRows(t, rows, literals)
		if extensionDump(t, p.Ctx, other) != dump {
			t.Fatal("independent symbolic replay changed")
		}
		run(fixture.Exec("UPDATE phon_source SET name='Schmidt' WHERE id=2", 1))
		run(fixture.Select("SELECT m,p,a FROM phon_values WHERE id=2", []string{"m", "p", "a"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"SKMTT", "XMT", "SMT"}}))
		run(fixture.Exec("INSERT INTO phon_source(id,name) VALUES(5,'gumbo')", 1))
		run(fixture.Select("SELECT d,m,p,a FROM phon_values WHERE id=5", []string{"d", "m", "p", "a"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"KM", "KM", "KMP", "KMP"}}))
		run(fixture.Select("SELECT id FROM phon_source WHERE phon_addon.dmetaphone_alt(name)='SMT' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		failure := metaphoneFailure("INSERT INTO phon_source(id,name,cap) VALUES(6,'gumbo',4),(7,'Smith',0)", "2200F", "output cannot be empty string")
		failure.SelectRows = false
		run(failure)
		run(fixture.Select("SELECT id FROM phon_source ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
	})
}
