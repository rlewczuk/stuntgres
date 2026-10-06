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

func TestReferenceStringLiterals(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,label TEXT,flag BOOL)", 0))
	want := [][]any{}
	for i, tc := range stringLiteralValues() {
		for _, prepared := range []bool{false, true} {
			id := len(want) + 1
			s := referenceExec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s,TRUE)", id, tc.source), 1)
			if prepared {
				s = referencePrepared(s)
			}
			p.step(t, s)
			want = append(want, []any{int64(id), tc.text, true})
		}
		if tc.source == `$$a;'b$$` {
			s := referenceSelect(`SELECT id FROM t WHERE label=$$a;'b$$ ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(2*i + 1)}, {int64(2*i + 2)}})
			p.step(t, s)
			p.step(t, referencePrepared(s))
		}
	}
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, want))
	p.step(t, referencePrepared(referenceSelect(`SELECT id FROM t WHERE label=E'a\nb' ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO t VALUES ($1,$2,E'\164')`, 1, 100, `a\nb`)))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO t VALUES ($1,$2,E'\164')`, 1, 101, `a\nb`)))
	p.step(t, referenceSelect("SELECT label,flag FROM t WHERE id=100", []string{"label", "flag"}, []string{"TEXT", "BOOL"}, [][]any{{`a\nb`, true}}))
	p.step(t, referenceExec("CREATE TABLE inputs(id INT,big BIGINT,flag BOOL)", 0))
	p.step(t, referenceExec(`INSERT INTO inputs VALUES (E'\061',E'\062',E'\164')`, 1))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO inputs VALUES (E'\061',E'\062',E'\164')`, 1)))
	p.step(t, referenceSelect("SELECT * FROM inputs", []string{"id", "big", "flag"}, []string{"INT4", "INT8", "BOOL"}, [][]any{{int64(1), int64(2), true}, {int64(1), int64(2), true}}))
	p.step(t, referenceExec("CREATE TABLE dollar_params(id INT,label TEXT)", 0))
	p.step(t, referenceExec(`INSERT INTO dollar_params VALUES ($1,$tag$$2;$tag$)`, 1, 7))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO dollar_params VALUES ($1,$tag$$2;$tag$)`, 1, 8)))
	p.step(t, referenceSelect("SELECT * FROM dollar_params ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "$2;"}, {int64(8), "$2;"}}))
	p.step(t, referencePrepared(referenceSelect(`SELECT id FROM dollar_params WHERE label=$$ $1 $$ ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{})))
	p.step(t, referenceExec("CREATE TABLE separated(x$tag$x$tag$ TEXT)", 0))
	p.step(t, referenceExec(`INSERT INTO separated(x$tag$x$tag$) VALUES ($$v$$)`, 1))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO separated(x$tag$x$tag$) VALUES ($$v$$)`, 1)))
	p.step(t, referenceSelect("SELECT x$tag$x$tag$ FROM separated ORDER BY x$tag$x$tag$", []string{"x$tag$x$tag$"}, []string{"TEXT"}, [][]any{{"v"}, {"v"}}))
	phantomNative := referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: "42P01", Message: `relation "phantom" does not exist`, Position: "15",
	}
	p.step(t, referenceStep{
		query: "SELECT * FROM phantom", selectRows: true, state: "42P01",
		diagnostic: &phantomNative, errorStage: "execute",
	})
	for _, prepared := range []bool{false, true} {
		s := referenceErrorDiagnostic("INSERT INTO t VALUES$$v$$", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "VALUES$$v$$"`, Position: "15"})
		s.errorStage = "execute"
		if prepared {
			s = referencePrepared(s)
			s.errorStage = "prepare"
		}
		p.step(t, s)
	}
	for _, tc := range []struct{ column, typ string }{{"id", "integer"}, {"big", "bigint"}, {"flag", "boolean"}} {
		q := "INSERT INTO inputs (" + tc.column + ") VALUES ('1'),(E'\\x71')"
		position := strconv.Itoa(strings.Index(q, "E'") + 1)
		p.step(t, referenceErrorDiagnostic(q, referenceDiagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
			Message: "invalid input syntax for type " + tc.typ + `: "q"`, Position: position,
		}))
		p.step(t, referenceSelect("SELECT * FROM inputs", []string{"id", "big", "flag"}, []string{"INT4", "INT8", "BOOL"}, [][]any{{int64(1), int64(2), true}, {int64(1), int64(2), true}}))
	}
	p.step(t, referenceExec("CREATE TABLE failures(label TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO failures VALUES ('seed')", 1))
	for _, tc := range stringLiteralErrors() {
		for _, prefix := range []string{"", "/*é*/ ", "/*😀*/ "} {
			position := tc.position
			if prefix != "" && position != "" {
				n, _ := strconv.Atoi(position)
				position = strconv.Itoa(n + 6)
			}
			q := prefix + "INSERT INTO t VALUES (" + tc.source
			// Lexical errors precede semantic row-width/type checking.
			diag := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint}
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic(q, diag)
				s.errorStage = "execute"
				if prepared {
					s = referencePrepared(s)
					s.errorStage = "prepare"
				}
				p.step(t, s)
			}
			queryStep := referenceErrorDiagnostic(q, diag)
			queryStep.selectRows = true
			queryStep.errorStage = "execute"
			p.step(t, queryStep)
		}
		q := "INSERT INTO failures VALUES ('valid'),(" + tc.source
		position := tc.position
		if position != "" {
			n, _ := strconv.Atoi(position)
			position = strconv.Itoa(n + 17)
		}
		diag := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: position, Hint: tc.hint}
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic(q, diag)
			s.errorStage = "execute"
			if prepared {
				s = referencePrepared(s)
				s.errorStage = "prepare"
			}
			p.step(t, s)
			p.step(t, referenceSelect("SELECT * FROM failures", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
		}
	}
	s := referenceErrorDiagnostic("CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES ($$a)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `unterminated dollar-quoted string at or near "$$a)"`, Position: "55"})
	s.errorStage = "execute"
	p.step(t, s)
	freshNative := referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: "42P01", Message: `relation "fresh" does not exist`, Position: "15",
	}
	p.step(t, referenceStep{
		query: "SELECT * FROM fresh", selectRows: true, state: "42P01",
		diagnostic: &freshNative, errorStage: "execute",
	})
	p.step(t, referenceSelect("SELECT * FROM failures", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
}

func TestReferenceStringLiteralDumpRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	for _, q := range []string{"CREATE TABLE t(id INT,label TEXT)", `INSERT INTO t VALUES (1,E'a\n\b\f\r\t\vO\'Brien\\tail'),(2,E'\303\251\uD83D\uDE00\uFFFF'),(3,E''),(4,NULL)`} {
		if _, err := p.native.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.native.ExecContext(p.ctx, "INSERT INTO t VALUES (5,$$a;'b$$),(6,$T$first\nsecond; O'Brien\\tail $1 $other$x$other$$T$),(7,$$$$),(8,$_9$é😀$_9$)"); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatalf("exact dump: %v\n%s", err, dump.String())
	}
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{
		{int64(1), "a\n\b\f\r\t\vO'Brien\\tail"}, {int64(2), "é😀\uFFFF"}, {int64(3), ""}, {int64(4), nil},
		{int64(5), "a;'b"}, {int64(6), "first\nsecond; O'Brien\\tail $1 $other$x$other$"}, {int64(7), ""}, {int64(8), "é😀"},
	}))
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("dump changed")
	}
}
