package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUnicodeEscapesValues(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, p, backend.PG)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, f.A, f.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,label TEXT)", 0))
			want := [][]any{}
			for _, tc := range unicodeEscapeValues() {
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s)", len(want)+1, tc.source), 1)
					s.Prepared = prepared
					run(s)
					want = append(want, []any{int64(len(want) + 1), tc.text})
				}
				ids := [][]any{}
				for _, row := range want {
					if row[1] == tc.text {
						ids = append(ids, []any{row[0]})
					}
				}
				s := fixture.Select("SELECT id FROM t WHERE label="+tc.source+" ORDER BY id", []string{"id"}, []string{"INT4"}, ids)
				fixture.RunStep(t, p.Ctx, f.B, f.Recorder, s)
				run(fixture.Prepared(s))
			}
			s := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, want)
			fixture.RunStep(t, p.Ctx, f.B, f.Recorder, s)
			rows, err := f.A.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LexicalRows(t, backend.PG, rows, s, nil)
			run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES ($1,$2)", 1, 100, `\0041`)))
			run(fixture.Select("SELECT label FROM t WHERE id=100", []string{"label"}, []string{"TEXT"}, [][]any{{`\0041`}}))
			run(fixture.Exec(`SET application_name TO U&'\0041'`, 0))
			run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"A"}}))
		})
	}
}

func TestReferenceUnicodeEscapesDiagnosticsAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, p, backend.PG)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, f.A, f.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(x TEXT NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO t VALUES ('seed')", 1))
			run(fixture.Exec("CREATE TABLE source_rows(x TEXT)", 0))
			run(fixture.Exec("INSERT INTO source_rows VALUES ('source')", 1))
			check := func(q string, diag fixture.Diagnostic, postgres *fixture.Diagnostic) {
				t.Helper()
				if backend.PG && postgres != nil {
					diag = *postgres
				}
				for _, op := range []string{"exec", "query", "prepare"} {
					s := fixture.ErrorDiagnostic(q, diag)
					s.SelectRows, s.Prepared, s.ErrorStage = op == "query", op == "prepare", "execute"
					if s.Prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					for _, conn := range []*sql.Conn{f.A, f.B} {
						fixture.RunStep(t, p.Ctx, conn, f.Recorder, fixture.Select("SELECT * FROM t", []string{"x"}, []string{"TEXT"}, [][]any{{"seed"}}))
						fixture.RunStep(t, p.Ctx, conn, f.Recorder, fixture.Select("SELECT * FROM source_rows", []string{"x"}, []string{"TEXT"}, [][]any{{"source"}}))
					}
				}
			}
			for _, tc := range unicodeEscapeErrors() {
				for _, prefix := range []string{"", "/*é*/ ", "/*😀*/ "} {
					n, _ := strconv.Atoi(tc.position)
					if prefix != "" {
						n += 6
					}
					check(prefix+"INSERT INTO t VALUES ("+tc.source, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: strconv.Itoa(n), Hint: tc.hint}, unicodeEscapePostgresDiagnostic(tc.source))
				}
				n, _ := strconv.Atoi(tc.position)
				check("INSERT INTO t VALUES ('valid'),("+tc.source, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: strconv.Itoa(n + 10), Hint: tc.hint}, unicodeEscapePostgresDiagnostic(tc.source))
			}
			for _, tc := range []struct{ q, message, position, hint string }{
				{`CREATE TABLE U&"a""\061"(x TEXT)`, "invalid Unicode escape", "19", `Unicode escapes must be \XXXX or \+XXXXXX.`},
				{`SELECT * FROM U&"a""😀\061"`, "invalid Unicode escape", "22", `Unicode escapes must be \XXXX or \+XXXXXX.`},
				{`CREATE TABLE U&""(x TEXT)`, `zero-length delimited identifier at or near "U&"""`, "14", ""},
				{`CREATE TABLE U&"abc`, `unterminated quoted identifier at or near "U&"abc"`, "14", ""},
				{`CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES (U&'\061')`, "invalid Unicode escape", "58", `Unicode escapes must be \XXXX or \+XXXXXX.`},
				{`CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES (U&'a''😀\061')`, "invalid Unicode escape", "62", `Unicode escapes must be \XXXX or \+XXXXXX.`},
				{`INSERT INTO t SELECT x FROM source_rows; INSERT INTO t VALUES (U&'\061')`, "invalid Unicode escape", "67", `Unicode escapes must be \XXXX or \+XXXXXX.`},
			} {
				check(tc.q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: tc.position, Hint: tc.hint}, unicodeEscapePostgresDiagnostic(tc.q))
			}
			const stagedSQL = `CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'a''😀\061')`
			stagedDiagnostic := fixture.Diagnostic{Code: "42601", Message: "invalid Unicode escape", Position: "101", Hint: `Unicode escapes must be \XXXX or \+XXXXXX.`}
			if backend.PG {
				stagedDiagnostic = *unicodeEscapePostgresDiagnostic(stagedSQL)
			}
			staged := fixture.ImplicitError(stagedSQL, stagedDiagnostic)
			staged.ErrorStage = "execute"
			run(staged)
			for _, name := range []string{"fresh", "staged"} {
				s := fixture.ImplicitError("SELECT * FROM "+name, fixture.Diagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"})
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, f.B, f.Recorder, s)
			}
		})
	}
}

func TestReferenceUnicodeEscapesNames(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, emoji := range []string{`U&'\D83D\DE00'`, `U&'\+01F600'`} {
			t.Run(fmt.Sprint(prepared)+"/"+emoji, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, tc := range unicodeEscapeNames() {
					quoted := `"` + strings.ReplaceAll(tc.name, `"`, `""`) + `"`
					for _, s := range []fixture.Step{
						fixture.Exec("CREATE TABLE "+tc.source+`(id INT,U&"\+01F600" TEXT)`, 0),
						fixture.Exec("INSERT INTO "+tc.source+` VALUES (1,U&'\0041')`, 1),
						fixture.Exec("INSERT INTO "+quoted+" VALUES (2,"+emoji+")", 1),
						fixture.Select("SELECT * FROM "+tc.source+" ORDER BY id", []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}),
						fixture.Select("SELECT * FROM "+quoted+" ORDER BY id", []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}),
					} {
						s.Prepared = prepared
						p.Step(t, s)
					}
					for _, backend := range p.Backends() {
						q := "SELECT * FROM " + tc.source + " ORDER BY id"
						stmt, err := backend.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						rows, err := stmt.QueryContext(p.Ctx)
						if err != nil {
							stmt.Close()
							t.Fatal(err)
						}
						fixture.LexicalRows(t, backend.PG, rows, fixture.Select(q, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}), nil)
						stmt.Close()
					}
				}
				native := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "a" does not exist`, Position: "15"}
				s := fixture.Step{Query: "SELECT * FROM a", SelectRows: true, State: "42P01", Diagnostic: &native, ErrorStage: "execute"}
				if prepared {
					s.Prepared = true
					s.ErrorStage = "prepare"
				}
				p.Step(t, s)
				p.Step(t, fixture.ErrorDiagnostic(`CREATE TABLE U&"`+strings.Repeat(`\0062`, 65)+`"(x TEXT)`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: `relation "` + strings.Repeat("b", 63) + `" already exists`}))
				p.Step(t, fixture.Select(`SELECT * FROM U&"\0070ublic".U&"\0041" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}))
			})
		}
	}
}

func TestReferenceUnicodeEscapesDumpRoundTrip(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT)`, 0))
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec(`INSERT INTO "A" VALUES (1,U&'\0041'),(2,U&'\D83D\DE00'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`, 5))
	s := fixture.Select(`SELECT * FROM "A" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}, {int64(3), ""}, {int64(4), nil}, {int64(5), "O'Brien\\\x01"}})
	held, err := p.Native.QueryContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, tc := range []struct{ script, position string }{
		{`CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'\061')`, "97"},
		{`CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'a''😀\061')`, "101"},
	} {
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script))
		want := fixture.ControlsReferenceDiagnostic("42601", "invalid Unicode escape")
		want.Position, want.Hint = tc.position, `Unicode escapes must be \XXXX or \+XXXXXX.`
		fixture.AssertDiagnostic(t, err, &want)
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
		missing := fixture.Error("SELECT * FROM staged", "42P01")
		missing.SelectRows = true
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, missing)
	}
	rows, err := p.Native.QueryContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	fixture.LexicalRows(t, false, rows, s, []bool{false, true})
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "U&") || !strings.Contains(dump.String(), `"A"`) || !strings.Contains(dump.String(), `"😀"`) {
		t.Fatalf("canonical names %s", dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("exact native dump %v\n%s", err, dump.String())
		}
	}
	p.Step(t, s)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("redump changed")
	}
	fixture.LexicalRows(t, false, held, s, []bool{false, true})
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	fixture.LexicalOrdinary(t, stuntgres.Dump(canceled, p.Native, io.Discard), context.Canceled)
	fixture.LexicalOrdinary(t, stuntgres.Reset(canceled, p.Native), context.Canceled)
	fixture.LexicalOrdinary(t, stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())), context.Canceled)
	reading, stop := context.WithCancel(p.Ctx)
	defer stop()
	fixture.LexicalOrdinary(t, stuntgres.Restore(reading, p.Native, &fixture.LexicalCancelReader{Cancel: stop}), context.Canceled)
	failure := errors.New("unicode control I/O")
	fixture.LexicalOrdinary(t, stuntgres.Restore(p.Ctx, p.Native, fixture.LexicalFailIO{failure}), failure)
	fixture.LexicalOrdinary(t, stuntgres.Dump(p.Ctx, p.Native, fixture.LexicalFailIO{failure}), failure)
	p.Step(t, s)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT); INSERT INTO U&"\0041" VALUES (1,U&'\0041'),(2,U&'\D83D\DE00'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`)); err != nil {
		t.Fatal(err)
	}
	p.Step(t, s)
}

func TestReferenceUnicodeEscapesComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(x TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES ('z'),(NULL),('a')", 3))
	for _, tc := range unicodeEscapeCompositionCases() {
		if tc.code != "42601" {
			continue
		}
		for _, op := range []string{"exec", "query", "prepare"} {
			t.Run(op+"/"+tc.query, func(t *testing.T) {
				s := fixture.ErrorDiagnostic(tc.query, fixture.Diagnostic{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code,
					Message: tc.message, Position: tc.position, Hint: tc.hint,
				})
				s.ErrorStage = "execute"
				if op == "query" {
					s.SelectRows = true
				}
				if op == "prepare" {
					s.Prepared = true
					s.ErrorStage = "prepare"
				}
				p.Step(t, s)
				p.Step(t, fixture.Select("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}}))
				for _, backend := range p.Backends() {
					peer, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					fixture.RunStep(t, p.Ctx, peer, backend.Recorder, fixture.Select("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}}))
					peer.Close()
				}
			})
		}
	}
	// Unicode-decoded keys use the same explicit NULL ordering on both backends.
	for _, tc := range []struct {
		query, position string
		rows            [][]any
	}{
		{`SELECT x FROM t ORDER BY U&"\0078" NULLS FIRST`, "36", [][]any{{nil}, {"a"}, {"z"}}},
		{`SELECT x FROM t ORDER BY U&"\0078" DESC NULLS LAST`, "41", [][]any{{"z"}, {"a"}, {nil}}},
	} {
		for _, prepared := range []bool{false, true} {
			step := fixture.Select(tc.query, []string{"x"}, []string{"TEXT"}, tc.rows)
			step.Prepared = prepared
			p.Step(t, step)
			p.Step(t, fixture.Select("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}}))
		}
	}
	supported := fixture.Select(`SELECT x FROM t ORDER BY U&"\0078"`, []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}})
	p.Step(t, supported)
	p.Step(t, fixture.Prepared(supported))
	for _, backend := range p.Backends() {
		rows, err := backend.DB.QueryContext(p.Ctx, supported.Query)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LexicalRows(t, backend.PG, rows, supported, nil)
	}
	p.Step(t, fixture.Exec(`CREATE TABLE U&"UESCAPE"(id INT,U&"FIRST" TEXT)`, 0))
	p.Step(t, fixture.Exec(`INSERT INTO U&"UESCAPE"(id,U&"FIRST") VALUES (1,U&'\0041')`, 1))
	p.Step(t, fixture.Prepared(fixture.Exec(`INSERT INTO "UESCAPE"(id,"FIRST") VALUES (2,U&'\+01F600')`, 1)))
	keywordRows := [][]any{{int64(1), "A"}, {int64(2), "😀"}}
	keywordSelect := fixture.Select(`SELECT id,U&"FIRST" FROM U&"UESCAPE" ORDER BY id`, []string{"id", "FIRST"}, []string{"INT4", "TEXT"}, keywordRows)
	p.Step(t, keywordSelect)
	p.Step(t, fixture.Prepared(keywordSelect))
	for _, backend := range p.Backends() {
		stmt, err := backend.DB.PrepareContext(p.Ctx, keywordSelect.Query)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			stmt.Close()
			t.Fatal(err)
		}
		fixture.LexicalRows(t, backend.PG, rows, keywordSelect, nil)
		stmt.Close()
	}
	p.Step(t, fixture.Select(`SELECT id,"FIRST" FROM "UESCAPE" ORDER BY id`, []string{"id", "FIRST"}, []string{"INT4", "TEXT"}, keywordRows))
}
