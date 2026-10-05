//go:build reference

package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceNumericLiteralsValues(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			step := func(s referenceStep) { t.Helper(); s.prepared = prepared; p.step(t, s) }
			step(referenceExec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
			step(referenceExec("INSERT INTO t VALUES (1,0b1010,0o12),(2,0xa,1_000)", 2))
			step(referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(1000)}}))
			p.step(t, referencePrepared(referenceExec("INSERT INTO t VALUES ($1,0B_1_010,0X_A)", 1, int64(3))))
			p.step(t, referencePrepared(referenceSelect("SELECT id,i,b FROM t WHERE i=0b1010 ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(1000)}, {int64(3), int64(10), int64(10)}})))
			p.step(t, referenceSelect("SELECT id,i,b FROM t WHERE b=1_000", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(10), int64(1000)}}))
			step(referenceExec("CREATE TABLE endpoints(id INT,b BIGINT)", 0))
			var want [][]any
			for i, literal := range numericEndpoints() {
				step(referenceExec(fmt.Sprintf("INSERT INTO endpoints VALUES (%d,%s)", i, literal), 1))
				value := int64(9223372036854775807)
				if i%2 == 1 {
					value = -9223372036854775808
				}
				want = append(want, []any{int64(i), value})
			}
			step(referenceSelect("SELECT id,b FROM endpoints ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, want))
			step(referenceExec("CREATE TABLE bounds(i INT,b BIGINT,label TEXT)", 0))
			step(referenceExec("INSERT INTO bounds VALUES (-0x8000_0000,0x8000_0000,0xa),(0x7FFF_FFFF,010,1_000)", 2))
			step(referenceSelect("SELECT i,b,label FROM bounds ORDER BY i", []string{"i", "b", "label"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(-2147483648), int64(2147483648), "10"}, {int64(2147483647), int64(10), "1000"}}))
		})
	}
}

func TestReferenceNumericLiteralsDiagnosticsAtomicity(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(x INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES (99)", 1))
	check := func(query, code, message, position string) {
		t.Helper()
		want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
		for _, operation := range []string{"exec", "query", "prepare"} {
			t.Run(operation+"/"+query, func(t *testing.T) {
				s := referenceErrorDiagnostic(query, want)
				s.errorStage = "execute"
				if operation == "prepare" {
					s.prepared = true
					s.errorStage = "prepare"
				}
				if operation == "query" {
					s.selectRows = true
				}
				p.step(t, s)
				p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(99)}}))
			})
		}
	}
	for _, c := range numericLiteralErrors() {
		message := c.reason + ` at or near "` + c.token + `"`
		check("INSERT INTO t VALUES ("+c.token+")", "42601", message, "23")
		check("INSERT INTO t VALUES (1),("+c.token+")", "42601", message, "27")
	}
	check("INSERT INTO t VALUES (-0b2)", "42601", `trailing junk after numeric literal at or near "0b2"`, "24")
	check("/*é*/ INSERT INTO t VALUES (0b2)", "42601", `trailing junk after numeric literal at or near "0b2"`, "29")
	check("/*😀*/ INSERT INTO t VALUES (0x_)", "42601", `invalid hexadecimal integer at or near "0x_"`, "29")
	for _, operation := range []string{"exec", "query"} {
		s := referenceErrorDiagnostic("INSERT INTO t VALUES (1); INSERT INTO t VALUES (0b2)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `trailing junk after numeric literal at or near "0b2"`, Position: "49"})
		s.selectRows = operation == "query"
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(99)}}))
	}
	// A representable int8 literal prepares successfully; assignment to int4
	// fails only at execution, without publishing the preceding valid row.
	for _, prepared := range []bool{false, true} {
		s := referenceErrorDiagnostic("INSERT INTO t VALUES (1),(0x8000_0000)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "integer out of range"})
		s.prepared = prepared
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(99)}}))
	}
	p.step(t, referenceSelect("SELECT x FROM t WHERE x=0x8000_0000", []string{"x"}, []string{"INT4"}, nil))
}

func TestReferenceNumericLiteralsDumpRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	script := "CREATE TABLE t(id INT,b BIGINT); INSERT INTO t VALUES (1,0b1010),(2,1_000),(3,0x7FFF_FFFF_FFFF_FFFF),(4,-0x8000_0000_0000_0000)"
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"10", "1000", "9223372036854775807", "-9223372036854775808"} {
		if !strings.Contains(dump.String(), value) {
			t.Fatalf("dump missing %s: %s", value, dump.String())
		}
	}
	if strings.Contains(dump.String(), "0x") || strings.Contains(dump.String(), "0b") || strings.Contains(dump.String(), "1_000") {
		t.Fatalf("noncanonical dump: %s", dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceSelect("SELECT id,b FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(1000)}, {int64(3), int64(9223372036854775807)}, {int64(4), int64(-9223372036854775808)}}))
}

func TestReferenceNumericLiteralsSpellings(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(id INT,b BIGINT)", 0))
			var want [][]any
			for i, c := range numericLiteralSpellings() {
				s := referenceExec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s)", i, c.literal), 1)
				s.prepared = prepared
				p.step(t, s)
				want = append(want, []any{int64(i), c.value})
			}
			s := referenceSelect("SELECT id,b FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, want)
			s.prepared = prepared
			p.step(t, s)
		})
	}
}
