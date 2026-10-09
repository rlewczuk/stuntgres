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
	"testing"
)

func TestReferenceUnicodeEscapesValues(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			f := newControlsReferenceFixture(t, p, backend.pg)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, f.a, f.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT,label TEXT)", 0))
			want := [][]any{}
			for _, tc := range unicodeEscapeValues() {
				for _, prepared := range []bool{false, true} {
					s := referenceExec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s)", len(want)+1, tc.source), 1)
					s.prepared = prepared
					run(s)
					want = append(want, []any{int64(len(want) + 1), tc.text})
				}
				ids := [][]any{}
				for _, row := range want {
					if row[1] == tc.text {
						ids = append(ids, []any{row[0]})
					}
				}
				s := referenceSelect("SELECT id FROM t WHERE label="+tc.source+" ORDER BY id", []string{"id"}, []string{"INT4"}, ids)
				referenceRunStep(t, p.ctx, f.b, f.recorder, s)
				run(referencePrepared(s))
			}
			s := referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, want)
			referenceRunStep(t, p.ctx, f.b, f.recorder, s)
			rows, err := f.a.QueryContext(p.ctx, s.query)
			if err != nil {
				t.Fatal(err)
			}
			lexicalRows(t, backend.pg, rows, s, nil)
			run(referencePrepared(referenceExec("INSERT INTO t VALUES ($1,$2)", 1, 100, `\0041`)))
			run(referenceSelect("SELECT label FROM t WHERE id=100", []string{"label"}, []string{"TEXT"}, [][]any{{`\0041`}}))
			run(referenceExec(`SET application_name TO U&'\0041'`, 0))
			run(referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"A"}}))
		})
	}
}

func TestReferenceUnicodeEscapesDiagnosticsAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			f := newControlsReferenceFixture(t, p, backend.pg)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, f.a, f.recorder, s) }
			run(referenceExec("CREATE TABLE t(x TEXT NOT NULL)", 0))
			run(referenceExec("INSERT INTO t VALUES ('seed')", 1))
			run(referenceExec("CREATE TABLE source_rows(x TEXT)", 0))
			run(referenceExec("INSERT INTO source_rows VALUES ('source')", 1))
			check := func(q string, diag referenceDiagnostic) {
				t.Helper()
				for _, op := range []string{"exec", "query", "prepare"} {
					s := referenceErrorDiagnostic(q, diag)
					s.selectRows, s.prepared, s.errorStage = op == "query", op == "prepare", "execute"
					if s.prepared {
						s.errorStage = "prepare"
					}
					run(s)
					for _, conn := range []*sql.Conn{f.a, f.b} {
						referenceRunStep(t, p.ctx, conn, f.recorder, referenceSelect("SELECT * FROM t", []string{"x"}, []string{"TEXT"}, [][]any{{"seed"}}))
						referenceRunStep(t, p.ctx, conn, f.recorder, referenceSelect("SELECT * FROM source_rows", []string{"x"}, []string{"TEXT"}, [][]any{{"source"}}))
					}
				}
			}
			for _, tc := range unicodeEscapeErrors() {
				for _, prefix := range []string{"", "/*é*/ ", "/*😀*/ "} {
					n, _ := strconv.Atoi(tc.position)
					if prefix != "" {
						n += 6
					}
					check(prefix+"INSERT INTO t VALUES ("+tc.source, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: strconv.Itoa(n), Hint: tc.hint})
				}
				n, _ := strconv.Atoi(tc.position)
				check("INSERT INTO t VALUES ('valid'),("+tc.source, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: strconv.Itoa(n + 10), Hint: tc.hint})
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
				check(tc.q, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: tc.position, Hint: tc.hint})
			}
			staged := referenceImplicitError(`CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'a''😀\061')`, referenceDiagnostic{Code: "42601", Message: "invalid Unicode escape", Position: "101", Hint: `Unicode escapes must be \XXXX or \+XXXXXX.`})
			staged.errorStage = "execute"
			run(staged)
			for _, name := range []string{"fresh", "staged"} {
				s := referenceImplicitError("SELECT * FROM "+name, referenceDiagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"})
				s.selectRows, s.errorStage = true, "execute"
				referenceRunStep(t, p.ctx, f.b, f.recorder, s)
			}
		})
	}
}

func TestReferenceUnicodeEscapesNames(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, emoji := range []string{`U&'\D83D\DE00'`, `U&'\+01F600'`} {
			t.Run(fmt.Sprint(prepared)+"/"+emoji, func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, tc := range unicodeEscapeNames() {
					quoted := `"` + strings.ReplaceAll(tc.name, `"`, `""`) + `"`
					for _, s := range []referenceStep{
						referenceExec("CREATE TABLE "+tc.source+`(id INT,U&"\+01F600" TEXT)`, 0),
						referenceExec("INSERT INTO "+tc.source+` VALUES (1,U&'\0041')`, 1),
						referenceExec("INSERT INTO "+quoted+" VALUES (2,"+emoji+")", 1),
						referenceSelect("SELECT * FROM "+tc.source+" ORDER BY id", []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}),
						referenceSelect("SELECT * FROM "+quoted+" ORDER BY id", []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}),
					} {
						s.prepared = prepared
						p.step(t, s)
					}
					for _, backend := range p.backends() {
						q := "SELECT * FROM " + tc.source + " ORDER BY id"
						stmt, err := backend.db.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						rows, err := stmt.QueryContext(p.ctx)
						if err != nil {
							stmt.Close()
							t.Fatal(err)
						}
						lexicalRows(t, backend.pg, rows, referenceSelect(q, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}), nil)
						stmt.Close()
					}
				}
				native := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "a" does not exist`, Position: "15"}
				s := referenceStep{query: "SELECT * FROM a", selectRows: true, state: "42P01", diagnostic: &native, errorStage: "execute"}
				if prepared {
					s.prepared = true
					s.errorStage = "prepare"
				}
				p.step(t, s)
				p.step(t, referenceErrorDiagnostic(`CREATE TABLE U&"`+strings.Repeat(`\0062`, 65)+`"(x TEXT)`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: `relation "` + strings.Repeat("b", 63) + `" already exists`}))
				p.step(t, referenceSelect(`SELECT * FROM U&"\0070ublic".U&"\0041" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}))
			})
		}
	}
}

func TestReferenceUnicodeEscapesDumpRoundTrip(t *testing.T) {
	p := newReferenceScenario(t)
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT)`, 0))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec(`INSERT INTO "A" VALUES (1,U&'\0041'),(2,U&'\D83D\DE00'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`, 5))
	s := referenceSelect(`SELECT * FROM "A" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}, {int64(3), ""}, {int64(4), nil}, {int64(5), "O'Brien\\\x01"}})
	held, err := p.native.QueryContext(p.ctx, s.query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, tc := range []struct{ script, position string }{
		{`CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'\061')`, "97"},
		{`CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'a''😀\061')`, "101"},
	} {
		err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(tc.script))
		want := controlsReferenceDiagnostic("42601", "invalid Unicode escape")
		want.Position, want.Hint = tc.position, `Unicode escapes must be \XXXX or \+XXXXXX.`
		referenceAssertDiagnostic(t, err, &want)
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s)
		missing := referenceError("SELECT * FROM staged", "42P01")
		missing.selectRows = true
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, missing)
	}
	rows, err := p.native.QueryContext(p.ctx, s.query)
	if err != nil {
		t.Fatal(err)
	}
	lexicalRows(t, false, rows, s, []bool{false, true})
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "U&") || !strings.Contains(dump.String(), `"A"`) || !strings.Contains(dump.String(), `"😀"`) {
		t.Fatalf("canonical names %s", dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatalf("exact native dump %v\n%s", err, dump.String())
		}
	}
	p.step(t, s)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("redump changed")
	}
	lexicalRows(t, false, held, s, []bool{false, true})
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	lexicalOrdinary(t, stuntgres.Dump(canceled, p.native, io.Discard), context.Canceled)
	lexicalOrdinary(t, stuntgres.Reset(canceled, p.native), context.Canceled)
	lexicalOrdinary(t, stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())), context.Canceled)
	reading, stop := context.WithCancel(p.ctx)
	defer stop()
	lexicalOrdinary(t, stuntgres.Restore(reading, p.native, &lexicalCancelReader{cancel: stop}), context.Canceled)
	failure := errors.New("unicode control I/O")
	lexicalOrdinary(t, stuntgres.Restore(p.ctx, p.native, lexicalFailIO{failure}), failure)
	lexicalOrdinary(t, stuntgres.Dump(p.ctx, p.native, lexicalFailIO{failure}), failure)
	p.step(t, s)
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT); INSERT INTO U&"\0041" VALUES (1,U&'\0041'),(2,U&'\D83D\DE00'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`)); err != nil {
		t.Fatal(err)
	}
	p.step(t, s)
}

func TestReferenceUnicodeEscapesComposition(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(x TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES ('z'),(NULL),('a')", 3))
	for _, tc := range unicodeEscapeCompositionCases() {
		if tc.code != "42601" {
			continue
		}
		for _, op := range []string{"exec", "query", "prepare"} {
			t.Run(op+"/"+tc.query, func(t *testing.T) {
				s := referenceErrorDiagnostic(tc.query, referenceDiagnostic{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code,
					Message: tc.message, Position: tc.position, Hint: tc.hint,
				})
				s.errorStage = "execute"
				if op == "query" {
					s.selectRows = true
				}
				if op == "prepare" {
					s.prepared = true
					s.errorStage = "prepare"
				}
				p.step(t, s)
				p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}}))
				for _, backend := range p.backends() {
					peer, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					referenceRunStep(t, p.ctx, peer, backend.recorder, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}}))
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
			step := referenceSelect(tc.query, []string{"x"}, []string{"TEXT"}, tc.rows)
			step.prepared = prepared
			p.step(t, step)
			p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}}))
		}
	}
	supported := referenceSelect(`SELECT x FROM t ORDER BY U&"\0078"`, []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}})
	p.step(t, supported)
	p.step(t, referencePrepared(supported))
	for _, backend := range p.backends() {
		rows, err := backend.db.QueryContext(p.ctx, supported.query)
		if err != nil {
			t.Fatal(err)
		}
		lexicalRows(t, backend.pg, rows, supported, nil)
	}
	p.step(t, referenceExec(`CREATE TABLE U&"UESCAPE"(id INT,U&"FIRST" TEXT)`, 0))
	p.step(t, referenceExec(`INSERT INTO U&"UESCAPE"(id,U&"FIRST") VALUES (1,U&'\0041')`, 1))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO "UESCAPE"(id,"FIRST") VALUES (2,U&'\+01F600')`, 1)))
	keywordRows := [][]any{{int64(1), "A"}, {int64(2), "😀"}}
	keywordSelect := referenceSelect(`SELECT id,U&"FIRST" FROM U&"UESCAPE" ORDER BY id`, []string{"id", "FIRST"}, []string{"INT4", "TEXT"}, keywordRows)
	p.step(t, keywordSelect)
	p.step(t, referencePrepared(keywordSelect))
	for _, backend := range p.backends() {
		stmt, err := backend.db.PrepareContext(p.ctx, keywordSelect.query)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.QueryContext(p.ctx)
		if err != nil {
			stmt.Close()
			t.Fatal(err)
		}
		lexicalRows(t, backend.pg, rows, keywordSelect, nil)
		stmt.Close()
	}
	p.step(t, referenceSelect(`SELECT id,"FIRST" FROM "UESCAPE" ORDER BY id`, []string{"id", "FIRST"}, []string{"INT4", "TEXT"}, keywordRows))
}
