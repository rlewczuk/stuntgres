//go:build reference

package reference_test

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceUnicodeEscapesValues(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,label TEXT)", 0))
	want := [][]any{}
	for _, tc := range unicodeEscapeValues() {
		for _, prepared := range []bool{false, true} {
			s := referenceExec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s)", len(want)+1, tc.source), 1)
			s.prepared = prepared
			p.step(t, s)
			want = append(want, []any{int64(len(want) + 1), tc.text})
		}
		ids := [][]any{}
		for _, row := range want {
			if row[1] == tc.text {
				ids = append(ids, []any{row[0]})
			}
		}
		s := referenceSelect("SELECT id FROM t WHERE label="+tc.source+" ORDER BY id", []string{"id"}, []string{"INT4"}, ids)
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, want))
	p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES ($1,$2)", 1, 100, `\0041`)))
	p.step(t, referenceSelect("SELECT label FROM t WHERE id=100", []string{"label"}, []string{"TEXT"}, [][]any{{`\0041`}}))
	p.step(t, referenceExec(`SET application_name TO U&'\0041'`, 0))
	p.step(t, referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"A"}}))
}

func TestReferenceUnicodeEscapesDiagnosticsAtomicity(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(x TEXT NOT NULL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES ('seed')", 1))
	p.step(t, referenceExec("CREATE TABLE source_rows(x TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO source_rows VALUES ('source')", 1))
	check := func(q string, diag referenceDiagnostic) {
		t.Helper()
		for _, op := range []string{"exec", "query", "prepare"} {
			s := referenceErrorDiagnostic(q, diag)
			s.errorStage = "execute"
			if op == "query" {
				s.selectRows = true
			}
			if op == "prepare" {
				s.prepared = true
				s.errorStage = "prepare"
			}
			p.step(t, s)
			p.step(t, referenceSelect("SELECT * FROM t", []string{"x"}, []string{"TEXT"}, [][]any{{"seed"}}))
			p.step(t, referenceSelect("SELECT * FROM source_rows", []string{"x"}, []string{"TEXT"}, [][]any{{"source"}}))
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
	check(`INSERT INTO t SELECT x FROM source_rows; INSERT INTO t VALUES (U&'\061')`, referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601",
		Message: "invalid Unicode escape", Position: "67", Hint: `Unicode escapes must be \XXXX or \+XXXXXX.`,
	})
	for _, tc := range []struct{ q, message, position, hint string }{
		{`CREATE TABLE U&"a""\061"(x TEXT)`, "invalid Unicode escape", "19", `Unicode escapes must be \XXXX or \+XXXXXX.`},
		{`SELECT * FROM U&"a""😀\061"`, "invalid Unicode escape", "22", `Unicode escapes must be \XXXX or \+XXXXXX.`},
		{`CREATE TABLE U&""(x TEXT)`, `zero-length delimited identifier at or near "U&"""`, "14", ""},
		{`CREATE TABLE U&"abc`, `unterminated quoted identifier at or near "U&"abc"`, "14", ""},
	} {
		check(tc.q, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Position: tc.position, Hint: tc.hint})
	}
	// PostgreSQL rejects prepared multiple commands independently; exercise its
	// direct script parse-before-execute and native's whole-script parsing.
	s := referenceErrorDiagnostic(`CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES (U&'\061')`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid Unicode escape", Position: "58", Hint: `Unicode escapes must be \XXXX or \+XXXXXX.`})
	s.errorStage = "execute"
	p.step(t, s)
	s = referenceErrorDiagnostic(`CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES (U&'a''😀\061')`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid Unicode escape", Position: "62", Hint: `Unicode escapes must be \XXXX or \+XXXXXX.`})
	s.errorStage = "execute"
	p.step(t, s)
	native := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "fresh" does not exist`}
	pg := native
	pg.Position = "15"
	p.step(t, referenceStep{query: "SELECT * FROM fresh", selectRows: true, state: "42P01", diagnostic: &native, postgresDiagnostic: &pg, errorStage: "execute"})
	// This is the exact malformed script used by the native Restore witness.
	// Exercise PostgreSQL's direct SQL path, not an invented Restore analogue,
	// to independently establish its full Position 101 record and no effects.
	staged := referenceErrorDiagnostic(`CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'a''😀\061')`, referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: "42601", Message: "invalid Unicode escape", Position: "101",
		Hint: `Unicode escapes must be \XXXX or \+XXXXXX.`,
	})
	staged.errorStage = "execute"
	p.step(t, staged)
	stagedNative := referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: "42P01", Message: `relation "staged" does not exist`,
	}
	stagedPostgres := stagedNative
	stagedPostgres.Position = "15"
	p.step(t, referenceStep{
		query: "SELECT * FROM staged", selectRows: true, state: "42P01",
		diagnostic: &stagedNative, postgresDiagnostic: &stagedPostgres, errorStage: "execute",
	})
	p.step(t, referenceSelect("SELECT * FROM t", []string{"x"}, []string{"TEXT"}, [][]any{{"seed"}}))
}

func TestReferenceUnicodeEscapesNames(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			for _, tc := range unicodeEscapeNames() {
				quoted := `"` + strings.ReplaceAll(tc.name, `"`, `""`) + `"`
				for _, s := range []referenceStep{
					referenceExec("CREATE TABLE "+tc.source+`(id INT,U&"\+01F600" TEXT)`, 0),
					referenceExec("INSERT INTO "+tc.source+` VALUES (1,U&'\0041')`, 1),
					referenceExec("INSERT INTO "+quoted+` VALUES (2,U&'\+01F600')`, 1),
					referenceSelect("SELECT * FROM "+tc.source+" ORDER BY id", []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}),
					referenceSelect("SELECT * FROM "+quoted+" ORDER BY id", []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}),
				} {
					s.prepared = prepared
					p.step(t, s)
				}
			}
			native := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "a" does not exist`}
			pg := native
			pg.Position = "15"
			s := referenceStep{query: "SELECT * FROM a", selectRows: true, state: "42P01", diagnostic: &native, postgresDiagnostic: &pg, errorStage: "execute"}
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

func TestReferenceUnicodeEscapesDumpRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	for _, q := range []string{
		`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT)`,
		`INSERT INTO "A" VALUES (1,U&'\0041'),(2,U&'\D83D\DE00'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`,
	} {
		if _, err := p.native.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "U&") || !strings.Contains(dump.String(), `"😀"`) {
		t.Fatalf("canonical names %s", dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatalf("exact native dump %v\n%s", err, dump.String())
	}
	p.step(t, referenceSelect(`SELECT * FROM "A" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}, {int64(3), ""}, {int64(4), nil}, {int64(5), "O'Brien\\\x01"}}))
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("redump changed")
	}
}

func TestReferenceUnicodeEscapesComposition(t *testing.T) {
	p := newReferencePair(t)
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
			})
		}
	}
	// PostgreSQL's valid NULL ordering and the native capability boundary
	// are independent outcomes, not normalized into fictitious parity.
	for _, tc := range []struct {
		query, position string
		rows            [][]any
	}{
		{`SELECT x FROM t ORDER BY U&"\0078" NULLS FIRST`, "36", [][]any{{nil}, {"a"}, {"z"}}},
		{`SELECT x FROM t ORDER BY U&"\0078" DESC NULLS LAST`, "41", [][]any{{"z"}, {"a"}, {nil}}},
	} {
		for _, prepared := range []bool{false, true} {
			native := referenceErrorDiagnostic(tc.query, referenceDiagnostic{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000",
				Message: "NULLS ordering is unsupported", Position: tc.position,
			})
			native.prepared, native.selectRows, native.errorStage = prepared, true, "execute"
			if prepared {
				native.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
			pg := referenceSelect(tc.query, []string{"x"}, []string{"TEXT"}, tc.rows)
			pg.prepared = prepared
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, pg)
			p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}}))
		}
	}
	supported := referenceSelect(`SELECT x FROM t ORDER BY U&"\0078"`, []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}})
	p.step(t, supported)
	p.step(t, referencePrepared(supported))
	p.step(t, referenceExec(`CREATE TABLE U&"UESCAPE"(id INT,U&"FIRST" TEXT)`, 0))
	p.step(t, referenceExec(`INSERT INTO U&"UESCAPE"(id,U&"FIRST") VALUES (1,U&'\0041')`, 1))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO "UESCAPE"(id,"FIRST") VALUES (2,U&'\+01F600')`, 1)))
	keywordRows := [][]any{{int64(1), "A"}, {int64(2), "😀"}}
	keywordSelect := referenceSelect(`SELECT id,U&"FIRST" FROM U&"UESCAPE" ORDER BY id`, []string{"id", "FIRST"}, []string{"INT4", "TEXT"}, keywordRows)
	p.step(t, keywordSelect)
	p.step(t, referencePrepared(keywordSelect))
	p.step(t, referenceSelect(`SELECT id,"FIRST" FROM "UESCAPE" ORDER BY id`, []string{"id", "FIRST"}, []string{"INT4", "TEXT"}, keywordRows))
}
