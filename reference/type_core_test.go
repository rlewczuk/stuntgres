//go:build reference

package reference_test

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestReferenceTypeCoreInputsInferenceAndMetadata(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE typed(i INTEGER NOT NULL,b BIGINT,t TEXT,f BOOLEAN)", 0))
	p.step(t, referenceExec("INSERT INTO typed VALUES(-2147483648,-9223372036854775808,TRUE,' ye '),(2147483647,9223372036854775807,FALSE,'of')", 2))
	p.step(t, referencePrepared(referenceExec("INSERT INTO typed VALUES($1,$2,$3,$4)", 1, "0x_FF", "0b10_10", []byte("owned"), "on")))
	for _, s := range []string{" +0007 ", "0o17", "1_000"} {
		p.step(t, referenceExec("INSERT INTO typed VALUES('"+s+"','"+s+"','x',NULL)", 1))
	}
	p.step(t, referenceSelect("SELECT * FROM typed ORDER BY i", []string{"i", "b", "t", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(-2147483648), int64(-9223372036854775808), "true", true}, {int64(7), int64(7), "x", nil}, {int64(15), int64(15), "x", nil}, {int64(255), int64(10), "owned", true}, {int64(1000), int64(1000), "x", nil}, {int64(2147483647), int64(9223372036854775807), "false", false}}))
	p.step(t, referenceExec("CREATE TABLE repeats(a INT,b TEXT,c INT)", 0))
	for _, n := range []int{7, 8} {
		p.step(t, referencePrepared(referenceExec("INSERT INTO repeats VALUES($1,$2,$1)", 1, n, "x")))
	}
	p.step(t, referenceSelect("SELECT * FROM repeats ORDER BY a", []string{"a", "b", "c"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(7), "x", int64(7)}, {int64(8), "x", int64(8)}}))
	p.step(t, referencePrepared(referenceSelect("SELECT i FROM typed WHERE i=2147483648", []string{"i"}, []string{"INT4"}, nil)))
	p.step(t, referencePrepared(referenceSelect("SELECT i FROM typed WHERE i=$1", []string{"i"}, []string{"INT4"}, nil, nil)))
}
func TestReferenceTypeCoreDiagnosticsAndAtomicity(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE failures(i INT NOT NULL,b BIGINT,t TEXT,f BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO failures VALUES(99,99,'sentinel',TRUE)", 1))
	unchanged := referenceSelect("SELECT * FROM failures", []string{"i", "b", "t", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(99), int64(99), "sentinel", true}})
	for _, tc := range []struct{ column, text, name, code string }{{"i", "oops", "integer", "22P02"}, {"i", "_1", "integer", "22P02"}, {"i", "1_", "integer", "22P02"}, {"i", "1__0", "integer", "22P02"}, {"i", "0b2", "integer", "22P02"}, {"i", "\u00a01", "integer", "22P02"}, {"i", "2147483648", "integer", "22003"}, {"i", "-2147483649", "integer", "22003"}, {"b", "9223372036854775808", "bigint", "22003"}, {"b", "-9223372036854775809", "bigint", "22003"}, {"i", "2147483648x", "integer", "22P02"}, {"b", "9223372036854775808x", "bigint", "22P02"}, {"i", "0x80000000x", "integer", "22P02"}, {"i", "999999999999999999999999x", "integer", "22003"}, {"i", "0xFFFFFFFFFFFFFFFFFFFFFFFFx", "integer", "22003"}, {"f", "o", "boolean", "22P02"}, {"f", "truth", "boolean", "22P02"}} {
		message := fmt.Sprintf("invalid input syntax for type %s: \"%s\"", tc.name, tc.text)
		if tc.code == "22003" {
			message = fmt.Sprintf("value \"%s\" is out of range for type %s", tc.text, tc.name)
		}
		q := "/*é*/ INSERT INTO failures(" + tc.column + ") VALUES('" + tc.text + "')"
		position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
		step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: message, Position: position})
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
		step.errorStage = "execute"
		p.step(t, step)
		bound := referenceImplicitError("INSERT INTO failures("+tc.column+") VALUES($1)", referenceDiagnostic{Code: tc.code, Message: message}, tc.text)
		pg := *bound.diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		bound.diagnostic = &pg
		bound.errorStage = "execute"
		p.step(t, bound)
		p.step(t, referencePrepared(bound))
		p.step(t, unchanged)
	}
	for _, tc := range []struct {
		value   int64
		message string
	}{{2147483648, `value "2147483648" is out of range for type integer`}, {-2147483649, `value "-2147483649" is out of range for type integer`}} {
		bound := referenceImplicitError("INSERT INTO failures(i) VALUES($1)", referenceDiagnostic{Code: "22003", Message: tc.message}, tc.value)
		pg := *bound.diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		bound.diagnostic = &pg
		bound.errorStage = "execute"
		p.step(t, bound)
		p.step(t, unchanged)
		p.step(t, referencePrepared(bound))
		p.step(t, unchanged)
	}
	q := "SELECT i FROM failures WHERE i=TRUE"
	step := referenceImplicitError(q, referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: strconv.Itoa(strings.Index(q, "=") + 1)})
	step.selectRows = true
	p.step(t, step)
	step.errorStage = "prepare"
	p.step(t, referencePrepared(step))
	q = "INSERT INTO failures(i) VALUES(TRUE)"
	step = referenceImplicitError(q, referenceDiagnostic{Code: "42804", Message: `column "i" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(strings.Index(q, "TRUE") + 1)})
	p.step(t, step)
	step.errorStage = "prepare"
	p.step(t, referencePrepared(step))
	for _, q := range []string{"INSERT INTO failures(i) VALUES(1),(2147483648)", "INSERT INTO failures(i) VALUES(1),(-2147483649)"} {
		step := referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, referencePrepared(step))
		p.step(t, unchanged)
	}
	q = "INSERT INTO failures(i) VALUES(1),(NULL)"
	step = referenceImplicitError(q, referenceDiagnostic{Code: "23502", Message: `null value in column "i" of relation "failures" violates not-null constraint`, Detail: "Failing row contains (null, null, null, null).", Schema: "public", Table: "failures", Column: "i"})
	step.errorStage = "execute"
	p.step(t, step)
	p.step(t, referencePrepared(step))
	p.step(t, unchanged)
}

func TestReferenceTypeCoreQuotedWhereDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE failures(i INTEGER,b BIGINT,f BOOLEAN)", 0))
	// Literal expectations are from root's pinned PostgreSQL 18.0 UTF8/C
	// empty-table captures, not native outcomes or computed parser offsets.
	for _, tc := range []struct{ column, text, code, message string }{
		{"i", "oops", "22P02", `invalid input syntax for type integer: "oops"`},
		{"f", "truth", "22P02", `invalid input syntax for type boolean: "truth"`},
		{"i", "2147483648", "22003", `value "2147483648" is out of range for type integer`},
	} {
		q := "/*é*/ SELECT " + tc.column + " FROM failures WHERE " + tc.column + "='" + tc.text + "'"
		step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: "38"})
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))

		bound := referenceImplicitError("/*é*/ SELECT "+tc.column+" FROM failures WHERE "+tc.column+"=$1", referenceDiagnostic{Code: tc.code, Message: tc.message}, tc.text)
		bound.selectRows = true
		bound.errorStage = "execute"
		pg := *bound.diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		bound.diagnostic = &pg
		p.step(t, bound)
		p.step(t, referencePrepared(bound))
	}
	p.step(t, referenceSelect("SELECT * FROM failures", []string{"i", "b", "f"}, []string{"INT4", "INT8", "BOOL"}, nil))
}
func TestReferenceTypeCoreDumpRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	script := "CREATE TABLE typed(i INT NOT NULL,b BIGINT,t TEXT,f BOOL); CREATE TABLE empty(i INT); INSERT INTO typed VALUES(-2147483648,-9223372036854775808,'O''Brien\n\\tail',TRUE),(0,NULL,NULL,NULL),(2147483647,9223372036854775807,'',FALSE)"
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
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
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceSelect("SELECT * FROM typed ORDER BY i", []string{"i", "b", "t", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(-2147483648), int64(-9223372036854775808), "O'Brien\n\\tail", true}, {int64(0), nil, nil, nil}, {int64(2147483647), int64(9223372036854775807), "", false}}))
	p.step(t, referenceSelect("SELECT * FROM empty", []string{"i"}, []string{"INT4"}, nil))
}
