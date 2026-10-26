package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func internalCharScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func internalCharQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Every CHAR cell is checked before fixture normalization: pq's OID18 decoder,
// unlike its generic textual decoder, returns string too.
func internalCharSelect(query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"v"}, []string{"CHAR"}, want, args...)
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if raw == nil {
			return
		}
		if _, ok := raw.(string); !ok {
			t.Fatalf("raw CHAR: %T %v, want string or NULL", raw, raw)
		}
	}
	return s
}

func TestReferenceInternalCharValues(t *testing.T) {
	internalCharScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE internal_char_values(id integer,v pg_catalog."char",u "char",c char(1))`, 0))
		vectors := [][2]string{
			{"r", "r"}, {"abc", "a"}, {" ab", " "}, {"", ""},
			{"é", `\303`}, {"界", `\347`}, {"😀", `\360`},
			{`\101`, "A"}, {`\000`, ""}, {`\177`, "\x7f"}, {`\200`, `\200`}, {`\377`, `\377`},
			{`\400`, ""}, {`\777`, `\377`}, {`\12`, `\`}, {`\378`, `\`}, {`\377x`, `\`},
			{"'", "'"}, {`\`, `\`},
		}
		var want [][]any
		for i, v := range vectors {
			run(internalCharSelect(`SELECT `+internalCharQuote(v[0])+`::pg_catalog."char" AS v`, [][]any{{v[1]}}))
			run(fixture.Exec(`INSERT INTO internal_char_values(id,v,u) VALUES($1,$2,$2)`, 1, int64(i+1), v[0]))
			want = append(want, []any{v[1]})
		}
		run(fixture.Exec(`INSERT INTO internal_char_values VALUES(100,NULL,NULL,'é')`, 1))
		want = append(want, []any{nil})
		run(internalCharSelect(`SELECT v FROM internal_char_values ORDER BY id`, want))
		run(internalCharSelect(`SELECT u AS v FROM internal_char_values ORDER BY id`, want))
		run(fixture.Select(`SELECT c FROM internal_char_values WHERE id=100`, []string{"c"}, []string{"BPCHAR"}, [][]any{{"é"}}))
		run(fixture.Exec(`INSERT INTO internal_char_values(id,v) SELECT 101,relkind FROM pg_class WHERE oid='internal_char_values'::regclass`, 1))
		run(internalCharSelect(`SELECT relkind AS v FROM pg_class WHERE oid='internal_char_values'::regclass`, [][]any{{"r"}}))
		run(internalCharSelect(`SELECT v FROM internal_char_values WHERE id=101`, [][]any{{"r"}}))
		run(fixture.Select(`SELECT oid::integer AS oid,typname,typlen FROM pg_type WHERE oid IN (18,1042) ORDER BY oid`, []string{"oid", "typname", "typlen"}, []string{"INT4", "NAME", "INT2"}, [][]any{{int64(18), "char", int64(1)}, {int64(1042), "bpchar", int64(-1)}}))
		run(fixture.Select(`SELECT attname,atttypid::integer AS oid FROM pg_attribute WHERE attrelid='internal_char_values'::regclass AND attnum>1 ORDER BY attnum`, []string{"attname", "oid"}, []string{"NAME", "INT4"}, [][]any{{"v", int64(18)}, {"u", int64(18)}, {"c", int64(1042)}}))
		d := fixture.ControlsReferenceDiagnostic("22001", "value too long for type character(1)")
		run(fixture.ErrorDiagnostic(`INSERT INTO internal_char_values(id,c) VALUES(102,'ab')`, d))
	})
}

func TestReferenceInternalCharPrepared(t *testing.T) {
	internalCharScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE internal_char_prepared(id integer,v "char")`, 0))
		q := `SELECT $1::pg_catalog."char" AS v`
		frames := []struct{ input, want any }{
			{"r", "r"}, {[]byte("abc"), "a"}, {"", ""}, {nil, nil}, {[]byte(nil), nil},
			{[]byte{}, ""}, {"é", `\303`}, {int64(65), "6"},
		}
		for _, prepared := range []bool{false, true} {
			for i, frame := range frames {
				s := internalCharSelect(q, [][]any{{frame.want}}, frame.input)
				s.Prepared = prepared
				run(s)
				s = fixture.Exec(`INSERT INTO internal_char_prepared VALUES($1,$2)`, 1, int64(i), frame.input)
				s.Prepared = prepared
				run(s)
			}
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, frame := range frames {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, internalCharSelect(q, [][]any{{frame.want}}, frame.input))
		}
		s := fixture.ErrorDiagnostic(q, internalCharEncodingDiagnostic("0xff", 1), "a\xff")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, internalCharSelect(q, [][]any{{"r"}}, "r"))
		insert := `INSERT INTO internal_char_prepared VALUES(100,$1)`
		writer, err := b.DB.PrepareContext(p.Ctx, insert)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		heldInsert := fixture.HeldStatement{Executor: b.DB, Statement: writer, SQL: insert}
		var want [][]any
		for _, frame := range frames {
			fixture.RunStep(t, p.Ctx, heldInsert, b.Recorder, fixture.Exec(insert, 1, frame.input))
			want = append(want, []any{frame.want})
		}
		s = fixture.ErrorDiagnostic(insert, internalCharEncodingDiagnostic("0x00", 1), "a\x00b")
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, heldInsert, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, heldInsert, b.Recorder, fixture.Exec(insert, 1, "r"))
		// Equal ids do not establish ordering, so assert the complete held-insert bag.
		want = append(want, []any{"r"})
		s = internalCharSelect(`SELECT v FROM internal_char_prepared WHERE id=100`, want)
		s.Bag = true
		run(s)
	})
}
