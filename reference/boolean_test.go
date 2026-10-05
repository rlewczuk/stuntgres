//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceBooleanDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE b(id INT,flag BOOL)", 0))
	diagnostic := func(message, position string) referenceDiagnostic {
		return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
			Message: message, Position: position}
	}
	literal := func(query, message, position string) {
		t.Helper()
		for _, operation := range []string{"exec", "query", "prepare"} {
			s := referenceErrorDiagnostic(query, diagnostic(message, position))
			s.selectRows = operation == "query"
			s.prepared = operation == "prepare"
			s.errorStage = "execute"
			if s.prepared {
				s.errorStage = "prepare"
			}
			p.step(t, s)
		}
	}
	literal("SELECT id FROM b WHERE flag='o'", `invalid input syntax for type boolean: "o"`, "29")
	p.step(t, referenceExec("INSERT INTO b VALUES (99,TRUE)", 1))
	sentinel := referenceSelect("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}})
	for _, input := range booleanInvalidInputs {
		literal("INSERT INTO b VALUES (1,"+input.source+")", input.message, "25")
		literal("SELECT id FROM b WHERE flag="+input.source, input.message, "29")
		p.step(t, sentinel)
	}
	for _, tc := range []struct{ query, message, position string }{
		{"/*é*/ INSERT INTO b VALUES (1,'o')", `invalid input syntax for type boolean: "o"`, "31"},
		{"/*é*/ SELECT id FROM b WHERE flag='o'", `invalid input syntax for type boolean: "o"`, "35"},
		{`SELECT id FROM b WHERE flag=E'\x6f'`, `invalid input syntax for type boolean: "o"`, "29"},
		{"SELECT id FROM b WHERE flag=$tag$o$tag$", `invalid input syntax for type boolean: "o"`, "29"},
		{"SELECT id FROM b WHERE flag='tr'\n'uth'", `invalid input syntax for type boolean: "truth"`, "29"},
		{"INSERT INTO b VALUES (1,TRUE),(2,'o')", `invalid input syntax for type boolean: "o"`, "34"},
	} {
		literal(tc.query, tc.message, tc.position)
		p.step(t, sentinel)
	}
	parameter := func(query, message, context string, args ...any) {
		t.Helper()
		for _, prepared := range []bool{false, true} {
			for _, selectRows := range []bool{false, true} {
				native := diagnostic(message, "")
				postgres := native
				postgres.Context = context
				s := referenceErrorDiagnostic(query, native, args...)
				s.postgresDiagnostic = &postgres
				s.prepared, s.selectRows, s.errorStage = prepared, selectRows, "execute"
				p.step(t, s)
			}
		}
	}
	for _, input := range booleanInvalidInputs {
		quoted := "'...'"
		if input.value == "" {
			quoted = "''"
		}
		for _, value := range []any{input.value, []byte(input.value)} {
			parameter("INSERT INTO b VALUES ($1,$2)", input.message, "unnamed portal parameter $2 = "+quoted, 1, value)
			parameter("SELECT id FROM b WHERE flag=$1", input.message, "unnamed portal parameter $1 = "+quoted, value)
			p.step(t, sentinel)
		}
	}
	parameter("INSERT INTO b VALUES (1,TRUE),(2,$1)", `invalid input syntax for type boolean: "o"`, "unnamed portal parameter $1 = '...'", "o")
	p.step(t, sentinel)
	p.step(t, referenceExec("CREATE TABLE repeated(flag BOOL)", 0))
	p.step(t, referenceExec("CREATE TABLE precedence(a BOOL,b BOOL)", 0))
	parameter("INSERT INTO repeated VALUES ($1),($1)", `invalid input syntax for type boolean: "o"`, "unnamed portal parameter $1 = '...'", "o")
	parameter("INSERT INTO precedence VALUES ($2,$1)", `invalid input syntax for type boolean: "truth"`, "unnamed portal parameter $1 = '...'", "truth", "o")
	p.step(t, referenceSelect("SELECT flag FROM repeated", []string{"flag"}, []string{"BOOL"}, nil))
	p.step(t, referenceSelect("SELECT a,b FROM precedence", []string{"a", "b"}, []string{"BOOL", "BOOL"}, nil))
	p.step(t, referenceExec("CREATE TABLE accepted(flag BOOL)", 0))
	for _, group := range []struct {
		values []string
		want   bool
	}{
		{[]string{"t", "tr", "tru", "true", "y", "ye", "yes", "on", "1", "TrUe", " YES ", "\tOn\n"}, true},
		{[]string{"f", "fa", "fal", "fals", "false", "n", "no", "of", "off", "0", "FaLsE", " NO ", "\tOfF\n"}, false},
	} {
		for index, value := range group.values {
			p.step(t, referencePrepared(referenceExec("INSERT INTO accepted VALUES ($1)", 1, []byte(value))))
			rows := make([][]any, index+1)
			for i := range rows {
				rows[i] = []any{group.want}
			}
			p.step(t, referenceSelect("SELECT flag FROM accepted WHERE flag=$1",
				[]string{"flag"}, []string{"BOOL"}, rows, value))
		}
	}
	p.step(t, referenceExec("INSERT INTO repeated VALUES (TRUE),(FALSE),(NULL)", 3))
	for _, value := range []any{true, false, nil, int64(1), int64(0)} {
		p.step(t, referenceExec("INSERT INTO repeated VALUES ($1)", 1, value))
	}
	p.step(t, referenceSelect("SELECT flag FROM repeated ORDER BY flag", []string{"flag"}, []string{"BOOL"},
		[][]any{{false}, {false}, {false}, {true}, {true}, {true}, {nil}, {nil}}))
	p.step(t, referenceSelect("SELECT flag FROM repeated WHERE flag=NULL", []string{"flag"}, []string{"BOOL"}, nil))
	// A successfully prepared handle survives conversion failure on each backend.
	for _, backend := range []struct {
		db       *sql.DB
		postgres bool
	}{{p.memory, false}, {p.postgres, true}} {
		stmt, err := backend.db.PrepareContext(p.ctx, "INSERT INTO b VALUES ($1,$2)")
		if err != nil {
			t.Fatal(err)
		}
		_, err = stmt.ExecContext(p.ctx, 1, "o")
		want := diagnostic(`invalid input syntax for type boolean: "o"`, "")
		if backend.postgres {
			want.Context = "unnamed portal parameter $2 = '...'"
		}
		referenceAssertDiagnostic(t, err, &want)
		for i, value := range []any{true, false, nil} {
			if _, err := stmt.ExecContext(p.ctx, i+1, value); err != nil {
				stmt.Close()
				t.Fatal(err)
			}
		}
		stmt.Close()
	}
	p.step(t, referenceSelect("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"},
		[][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(99), true}}))
	// Controls have no PostgreSQL API analogue: assert native staging, then execute
	// the exact native dump on PostgreSQL as the independent serialization witness.
	cursor, err := p.native.QueryContext(p.ctx, "SELECT flag FROM b ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(flag BOOL); INSERT INTO replacement VALUES (TRUE),('o');"))
	referenceAssertDiagnostic(t, err, &referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
		Message: `invalid input syntax for type boolean: "o"`, Position: "77",
	})
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect(
		"SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"},
		[][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(99), true}}))
	_, err = p.native.QueryContext(p.ctx, "SELECT flag FROM replacement")
	referenceState(t, err, "42P01")
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
	if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE b,accepted,repeated,precedence"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceSelect("SELECT flag FROM b ORDER BY id", []string{"flag"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}, {true}}))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatalf("boolean redump changed: %v", err)
	}
	for _, want := range []any{true, false, nil, true} {
		if !cursor.Next() {
			t.Fatalf("retained cursor ended: %v", cursor.Err())
		}
		var got any
		if err := cursor.Scan(&got); err != nil || got != want {
			t.Fatalf("retained bool = %#v, %v", got, err)
		}
	}
	if cursor.Next() || cursor.Err() != nil {
		t.Fatalf("retained cursor tail: %v", cursor.Err())
	}
}
