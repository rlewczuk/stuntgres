//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"stuntgres"
	"testing"
)

func TestReferenceIdentifierKeywords(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE abort (by INT, between TEXT, values BOOL, integer BIGINT, char TEXT)", 0),
		referencePrepared(referenceExec("CREATE TABLE insert (by INT)", 0)),
		referenceExec("INSERT INTO abort (by,between,values,integer,char) VALUES (1,'one',TRUE,10,'a'),(2,'two',FALSE,20,'b')", 2),
		referencePrepared(referenceExec("INSERT INTO public.abort (by,between,values,integer,char) VALUES ($1,$2,$3,$4,$5)", 1, 3, "three", true, 30, "c")),
		referenceSelect("SELECT by,between,values,integer,char FROM public.abort WHERE by=2 ORDER BY by DESC", []string{"by", "between", "values", "integer", "char"}, []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}, [][]any{{int64(2), "two", false, int64(20), "b"}}),
		referencePrepared(referenceSelect("SELECT by,between,values,integer,char FROM abort WHERE by=$1 ORDER BY by DESC", []string{"by", "between", "values", "integer", "char"}, []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}, [][]any{{int64(2), "two", false, int64(20), "b"}}, 2)),
		referenceExec("INSERT INTO insert (by) VALUES (7)", 1),
		referenceSelect("SELECT by FROM insert ORDER BY by", []string{"by"}, []string{"INT4"}, [][]any{{int64(7)}}),
		referenceExec(`CREATE TABLE "select" ("join" INT,"authorization" TEXT,"a""b" BOOL)`, 0),
		referenceExec(`INSERT INTO "select" VALUES (7,'kept',TRUE)`, 1),
		referenceSelect(`SELECT "join","authorization","a""b" FROM "select" ORDER BY "join"`, []string{"join", "authorization", "a\"b"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "kept", true}}),
	} {
		p.step(t, s)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), p.native, &dump); err != nil {
		t.Fatal(err)
	}
	// Restore uses the existing native seam; PostgreSQL executes that exact dump.
	if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE abort,insert,\"select\""); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(context.Background(), p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), p.native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("redump differs")
	}
	p.step(t, referenceSelect("SELECT by,between,values,integer,char FROM abort ORDER BY by", []string{"by", "between", "values", "integer", "char"}, []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}, [][]any{{int64(1), "one", true, int64(10), "a"}, {int64(2), "two", false, int64(20), "b"}, {int64(3), "three", true, int64(30), "c"}}))
}
func TestReferenceIdentifierDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range identifierSyntaxCases {
		want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: fmt.Sprintf("syntax error at or near \"%s\"", s.word), Position: s.pos}
		for _, mode := range []string{"exec", "query", "prepare"} {
			step := referenceErrorDiagnostic(s.q, want)
			step.errorStage = "execute"
			if mode == "query" {
				step.selectRows = true
			}
			if mode == "prepare" {
				step.prepared = true
				step.errorStage = "prepare"
			}
			p.step(t, step)
		}
	}
}
func TestReferenceIdentifierLabels(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t (x INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES (7)", 1))
	for _, s := range []struct{ label, column, pos string }{{"check", "check", "10"}, {"default", "default", "10"}, {"authorization", "authorization", "10"}, {"AS char", "char", "13"}, {"AS grant", "grant", "13"}, {"AS \"Mixed\"", "Mixed", "13"}} {
		q := "SELECT x " + s.label + " FROM t"
		for _, prepared := range []bool{false, true} {
			native := referenceErrorDiagnostic(q, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "projection aliases are unsupported", Position: s.pos})
			native.prepared = prepared
			native.selectRows = true
			native.errorStage = "execute"
			if prepared {
				native.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
			pg := referenceSelect(q, []string{s.column}, []string{"INT4"}, [][]any{{int64(7)}})
			pg.prepared = prepared
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, pg)
		}
	}
	for _, word := range []string{"char", "day"} {
		step := referenceErrorDiagnostic("SELECT x "+word+" FROM t", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: fmt.Sprintf("syntax error at or near \"%s\"", word), Position: "10"})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
func TestReferenceIdentifierKeywordInventory(t *testing.T) {
	p := newReferencePair(t)
	rows, err := p.postgres.QueryContext(p.ctx, "SELECT word,catcode,barelabel,catdesc,baredesc FROM pg_get_keywords() ORDER BY word")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	total, bareCount := 0, 0
	previous := ""
	samples := map[string][]any{}
	descriptions := map[string]string{"U": "unreserved", "C": "unreserved (cannot be function or type name)", "T": "reserved (can be function or type name)", "R": "reserved"}
	for rows.Next() {
		var word, cat, desc, baredesc string
		var bare bool
		if err := rows.Scan(&word, &cat, &bare, &desc, &baredesc); err != nil {
			t.Fatal(err)
		}
		if word <= previous || descriptions[cat] != desc {
			t.Fatalf("inventory %s %s %s", word, cat, desc)
		}
		previous = word
		wantBare := "requires AS"
		if bare {
			wantBare = "can be bare label"
			bareCount++
		}
		if baredesc != wantBare {
			t.Fatalf("bare description %s", word)
		}
		counts[cat]++
		total++
		samples[word] = []any{cat, bare}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if total != 494 || bareCount != 455 || !reflect.DeepEqual(counts, map[string]int{"U": 330, "C": 63, "T": 23, "R": 78}) {
		t.Fatalf("inventory total=%d bare=%d counts=%v", total, bareCount, counts)
	}
	for _, s := range []struct {
		word, cat string
		bare      bool
	}{
		{"abort", "U", true}, {"by", "U", true}, {"insert", "U", true}, {"text", "U", true}, {"merge", "U", true}, {"virtual", "U", true}, {"between", "C", true}, {"bigint", "C", true}, {"integer", "C", true}, {"values", "C", true}, {"json", "C", true}, {"char", "C", false}, {"day", "U", false}, {"authorization", "T", true}, {"join", "T", true}, {"isnull", "T", false}, {"select", "R", true}, {"check", "R", true}, {"analyze", "R", true}, {"end", "R", true}, {"do", "R", true}, {"grant", "R", false}, {"from", "R", false}, {"where", "R", false}, {"order", "R", false},
		{"notnull", "T", false}, {"default", "R", true},
	} {
		if !reflect.DeepEqual(samples[s.word], []any{s.cat, s.bare}) {
			t.Fatalf("sample %s=%v", s.word, samples[s.word])
		}
	}
	for _, word := range []string{"bool", "int4", "int8", "date", "uuid", "serial", "jsonb", "bytea", "float4", "float8"} {
		if _, ok := samples[word]; ok {
			t.Fatalf("unexpected keyword %s", word)
		}
	}
}

func TestReferenceIdentifierNormalization(t *testing.T) {
	for _, s := range identifierNormalizationCases {
		t.Run(s.name, func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE "+s.spelling+" ("+s.spelling+" INT)", 0))
			p.step(t, referencePrepared(referenceExec("INSERT INTO "+s.spelling+" ("+s.spelling+") VALUES ($1)", 1, 7)))
			p.step(t, referenceSelect("SELECT "+s.spelling+" FROM "+s.spelling+" ORDER BY "+s.spelling, []string{s.name}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceIdentifierQualifiedNames(t *testing.T) {
	p := newReferencePair(t)
	for i, name := range []string{"select", "authorization", "from", "check"} {
		create := referenceExec("CREATE TABLE public."+name+" (x INT)", 0)
		if i%2 == 1 {
			create = referencePrepared(create)
		}
		p.step(t, create)
		p.step(t, referencePrepared(referenceExec("INSERT INTO public."+name+" (x) VALUES ($1)", 1, 7+i)))
		selectStep := referenceSelect("SELECT x FROM public."+name+" WHERE x=$1 ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(7 + i)}}, 7+i)
		p.step(t, selectStep)
		p.step(t, referencePrepared(selectStep))
		p.step(t, referenceSelect("SELECT x FROM \""+name+"\" ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(7 + i)}}))
	}
}

func TestReferenceIdentifierInsertQueries(t *testing.T) {
	for _, s := range []struct {
		q   string
		row any
	}{
		{"INSERT INTO t (SELECT)", nil},
		{"INSERT INTO t SELECT x FROM source_rows", int64(7)},
		{"INSERT INTO t ((SELECT x FROM source_rows WHERE x=7 ORDER BY x))", int64(7)},
		{"INSERT INTO t (x) SELECT x FROM source_rows", int64(7)},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", s.q, prepared), func(t *testing.T) {
				p := newReferencePair(t)
				p.step(t, referenceExec("CREATE TABLE t (x INT)", 0))
				p.step(t, referenceExec("CREATE TABLE source_rows (x INT)", 0))
				p.step(t, referenceExec("INSERT INTO source_rows VALUES (7)", 1))
				p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, nil))
				step := referenceExec(s.q, 1)
				step.prepared = prepared
				p.step(t, step)
				p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{s.row}}))
			})
		}
	}
}

func TestReferenceIdentifierContextualCapabilities(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t (x INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES (7)", 1))
	for _, s := range []struct {
		q     string
		value bool
	}{
		{"SELECT x isnull FROM t", false},
		{"SELECT x notnull FROM t", true},
	} {
		step := referenceSelect(s.q, []string{"?column?"}, []string{"BOOL"}, [][]any{{s.value}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, name := range []string{"f", "abort", "authorization", `"between"`} {
		if _, err := p.postgres.ExecContext(p.ctx, "CREATE FUNCTION public."+name+"(integer) RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT $1'"); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range identifierContextCases {
		for _, prepared := range []bool{false, true} {
			native := referenceErrorDiagnostic(s.q, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: s.code, Message: s.message, Position: s.position})
			native.prepared = prepared
			native.selectRows = true
			native.errorStage = "execute"
			if prepared {
				native.errorStage = "prepare"
			}
			if s.code == "42601" {
				p.step(t, native)
				continue
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
			pg := referenceSelect(s.q, []string{s.column}, []string{s.typ}, [][]any{{s.row}})
			pg.prepared = prepared
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, pg)
		}
	}
}

func TestReferenceIdentifierNullExpressions(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t (x INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES (7),(NULL)", 2))
	for _, s := range []struct {
		q    string
		rows [][]any
	}{
		{"SELECT x isnull FROM t ORDER BY x", [][]any{{false}, {true}}},
		{"SELECT x notnull FROM t ORDER BY x", [][]any{{true}, {false}}},
	} {
		step := referenceSelect(s.q, []string{"?column?"}, []string{"BOOL"}, s.rows)
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, s := range []struct {
		q, msg, pos, column, typ string
		rows                     [][]any
	}{
		{"SELECT x FROM t ORDER BY x NULLS FIRST", "NULLS ordering is unsupported", "28", "x", "INT4", [][]any{{nil}, {int64(7)}}},
		{"SELECT x FROM t ORDER BY x DESC NULLS LAST", "NULLS ordering is unsupported", "33", "x", "INT4", [][]any{{int64(7)}, {nil}}},
	} {
		for _, prepared := range []bool{false, true} {
			native := referenceErrorDiagnostic(s.q, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: s.msg, Position: s.pos})
			native.prepared = prepared
			native.selectRows = true
			native.errorStage = "execute"
			if prepared {
				native.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
			pg := referenceSelect(s.q, []string{s.column}, []string{s.typ}, s.rows)
			pg.prepared = prepared
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, pg)
		}
	}
	bad := referenceErrorDiagnostic("SELECT x FROM t ORDER BY x NULLS bogus", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"NULLS\"", Position: "28"})
	p.step(t, bad)
	p.step(t, referencePrepared(bad))
}

func TestReferenceIdentifierLaterProjectionCalls(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t (x INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES (7)", 1))
	for _, name := range []string{"abort", "authorization", `"between"`} {
		if _, err := p.postgres.ExecContext(p.ctx, "CREATE FUNCTION public."+name+"(integer) RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT $1'"); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range identifierLaterCallCases {
		for _, prepared := range []bool{false, true} {
			native := referenceErrorDiagnostic(s.q, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: s.code, Message: s.message, Position: s.position})
			native.selectRows = true
			native.prepared = prepared
			native.errorStage = "execute"
			if prepared {
				native.errorStage = "prepare"
			}
			if s.code == "42601" {
				p.step(t, native)
				continue
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
			pg := referenceSelect(s.q, s.columns, s.types, s.rows)
			pg.prepared = prepared
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, pg)
		}
	}
	p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
}
