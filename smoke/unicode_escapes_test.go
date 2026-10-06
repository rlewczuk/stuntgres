package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func TestUnicodeEscapesSmoke(t *testing.T) {
	if err := unicodeEscapesRun(); err != nil {
		t.Fatal(err)
	}
}

func unicodeEscapesDiagnostic(err error, message, position, hint string) error {
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: message, Position: position, Hint: hint}
	var got *stuntgres.Error
	if !errors.As(err, &got) || *got != want || got.SQLState() != want.Code {
		return fmt.Errorf("diagnostic %#v %v; want %#v", got, err, want)
	}
	return nil
}

func unicodeEscapesOrdinaryCanceled(err error) error {
	var state interface{ SQLState() string }
	if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
		return fmt.Errorf("ordinary cancellation: %v", err)
	}
	return nil
}

func unicodeEscapesVerifyRows(rows *sql.Rows, names, types []string, want [][]any) error {
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, names) {
		return fmt.Errorf("columns %v: %v; want %v", columns, err, names)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	if len(metadata) != len(types) {
		return fmt.Errorf("metadata width %d", len(metadata))
	}
	for i, typ := range types {
		if metadata[i].DatabaseTypeName() != typ {
			return fmt.Errorf("type %d: %s", i, metadata[i].DatabaseTypeName())
		}
	}
	got := [][]any{}
	targets := make([]any, len(names))
	for rows.Next() {
		values := make([]any, len(names))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return err
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("rows %#v; want %#v", got, want)
	}
	return nil
}

func unicodeEscapesRun() error {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector("unicode-escapes-smoke")
	if err != nil {
		return err
	}
	first, second := sql.OpenDB(connector), sql.OpenDB(connector)
	defer first.Close()
	defer second.Close()
	if err := second.PingContext(ctx); err != nil {
		return err
	}
	exec := func(q string, prepared bool, affected int64) error {
		var result sql.Result
		var err error
		if prepared {
			s, e := first.PrepareContext(ctx, q)
			if e != nil {
				return e
			}
			result, err = s.ExecContext(ctx)
			s.Close()
		} else {
			result, err = first.ExecContext(ctx, q)
		}
		if err != nil {
			return err
		}
		if n, e := result.RowsAffected(); e != nil || n != affected {
			return fmt.Errorf("affected %d: %v", n, e)
		}
		return nil
	}
	if err := exec(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT)`, false, 0); err != nil {
		return err
	}
	want := [][]any{}
	for i, tc := range []struct {
		source, text string
		prepared     bool
	}{
		{`U&'\0041'`, "A", false}, {`U&'\+01F600'`, "😀", true},
		{`U&'\D83D\DE00'`, "😀", true}, {`U&'!0041' UESCAPE '!'`, "A", false},
		{`U&''`, "", true}, {`NULL`, "", false}, {`U&'O''Brien\\\0001'`, "O'Brien\\\x01", false},
	} {
		if err := exec(fmt.Sprintf(`INSERT INTO U&"\0041" VALUES (%d,%s)`, i+1, tc.source), tc.prepared, 1); err != nil {
			return err
		}
		var text any = tc.text
		if tc.source == "NULL" {
			text = nil
		}
		want = append(want, []any{int64(i + 1), text})
	}
	verify := func(db *sql.DB) error {
		r, err := db.QueryContext(ctx, `SELECT * FROM "A" ORDER BY id`)
		if err != nil {
			return err
		}
		metadata, err := r.ColumnTypes()
		if err != nil {
			r.Close()
			return err
		}
		if len(metadata) != 2 {
			r.Close()
			return fmt.Errorf("Unicode metadata width %d", len(metadata))
		}
		nullable, known := metadata[0].Nullable()
		if !known || nullable {
			r.Close()
			return fmt.Errorf("Unicode id lost NOT NULL metadata")
		}
		return unicodeEscapesVerifyRows(r, []string{"id", "😀"}, []string{"INT4", "TEXT"}, want)
	}
	for _, db := range []*sql.DB{first, second} {
		if err := verify(db); err != nil {
			return err
		}
	}
	escapedRows, err := first.QueryContext(ctx, `SELECT * FROM U&"\0041" ORDER BY id`)
	if err != nil {
		return err
	}
	if err := unicodeEscapesVerifyRows(escapedRows, []string{"id", "😀"}, []string{"INT4", "TEXT"}, want); err != nil {
		return err
	}
	for _, prepared := range []bool{false, true} {
		q := `SELECT id FROM U&"\0041" WHERE U&"\+01F600"=U&'\D83D\DE00' ORDER BY id`
		var rows *sql.Rows
		if prepared {
			s, e := first.PrepareContext(ctx, q)
			if e != nil {
				return e
			}
			rows, err = s.QueryContext(ctx)
			if err == nil {
				err = unicodeEscapesVerifyRows(rows, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
			}
			s.Close()
		} else {
			rows, err = first.QueryContext(ctx, q)
			if err == nil {
				err = unicodeEscapesVerifyRows(rows, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}})
			}
		}
		if err != nil {
			return err
		}
	}
	r, err := first.QueryContext(ctx, "SELECT * FROM a")
	if r != nil {
		r.Close()
		return fmt.Errorf("quoted case was lost")
	}
	var missing *stuntgres.Error
	missingWant := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "a" does not exist`, Position: "15"}
	if !errors.As(err, &missing) || *missing != missingWant {
		return fmt.Errorf("quoted lookup %#v %v", missing, err)
	}
	if err := exec("CREATE TABLE t(x TEXT NOT NULL)", false, 0); err != nil {
		return err
	}
	if err := exec("INSERT INTO t VALUES ('seed')", false, 1); err != nil {
		return err
	}
	if err := exec("CREATE TABLE source_rows(x TEXT)", false, 0); err != nil {
		return err
	}
	if err := exec("INSERT INTO source_rows VALUES ('source')", false, 1); err != nil {
		return err
	}
	check := func(q, message, position, hint string) error {
		for _, op := range []string{"exec", "query", "prepare"} {
			var err error
			switch op {
			case "exec":
				result, e := first.ExecContext(ctx, q)
				err = e
				if result != nil {
					return fmt.Errorf("bad SQL yielded result")
				}
			case "query":
				r, e := first.QueryContext(ctx, q)
				err = e
				if r != nil {
					r.Close()
					return fmt.Errorf("bad SQL yielded cursor")
				}
			case "prepare":
				s, e := first.PrepareContext(ctx, q)
				err = e
				if s != nil {
					s.Close()
					return fmt.Errorf("bad SQL prepared")
				}
			}
			if err := unicodeEscapesDiagnostic(err, message, position, hint); err != nil {
				return fmt.Errorf("%s %q: %w", op, q, err)
			}
		}
		return nil
	}
	const hint = `Unicode escapes must be \XXXX or \+XXXXXX.`
	for _, tc := range []struct{ q, message, position, hint string }{
		{`INSERT INTO t VALUES (U&'\061')`, "invalid Unicode escape", "26", hint},
		{`INSERT INTO t VALUES (U&'\D800')`, "invalid Unicode surrogate pair", "31", ""},
		{`INSERT INTO t VALUES (U&'\DC00')`, "invalid Unicode surrogate pair", "26", ""},
		{`INSERT INTO t VALUES (U&'x' UESCAPE '+')`, `invalid Unicode escape character at or near "'+'"`, "37", ""},
		{`INSERT INTO t VALUES (U&'a''\061')`, "invalid Unicode escape", "28", hint},
		{`INSERT INTO t VALUES (U&'a''😀\061')`, "invalid Unicode escape", "30", hint},
		{`INSERT INTO t VALUES (U&'\061' /*`, `unterminated /* comment at or near "/*"`, "32", ""},
		{`INSERT INTO t VALUES ('valid'),(U&'\061')`, "invalid Unicode escape", "36", hint},
		{`CREATE TABLE fresh(x TEXT); INSERT INTO fresh VALUES (U&'\061')`, "invalid Unicode escape", "58", hint},
		{`SELECT x FROM t ORDER BY x NULLS U&"FIRST"`, `syntax error at or near "NULLS"`, "28", ""},
		{`INSERT INTO t SELECT x FROM source_rows; INSERT INTO t VALUES (U&'\061')`, "invalid Unicode escape", "67", hint},
	} {
		if err := check(tc.q, tc.message, tc.position, tc.hint); err != nil {
			return err
		}
		for _, db := range []*sql.DB{first, second} {
			r, err := db.QueryContext(ctx, "SELECT * FROM t")
			if err != nil {
				return err
			}
			if err := unicodeEscapesVerifyRows(r, []string{"x"}, []string{"TEXT"}, [][]any{{"seed"}}); err != nil {
				return err
			}
			source, err := db.QueryContext(ctx, "SELECT x FROM source_rows")
			if err != nil {
				return err
			}
			if err := unicodeEscapesVerifyRows(source, []string{"x"}, []string{"TEXT"}, [][]any{{"source"}}); err != nil {
				return err
			}
		}
	}
	r, err = second.QueryContext(ctx, "SELECT * FROM fresh")
	if r != nil {
		r.Close()
		return fmt.Errorf("bad suffix published fresh")
	}
	missingWant.Message = `relation "fresh" does not exist`
	if !errors.As(err, &missing) || *missing != missingWant {
		return fmt.Errorf("fresh lookup %#v %v", missing, err)
	}
	held, err := second.QueryContext(ctx, `SELECT * FROM "A" ORDER BY id`)
	if err != nil {
		return err
	}
	defer held.Close()
	bad := `CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'\061')`
	if err := unicodeEscapesDiagnostic(stuntgres.Restore(ctx, first, bytes.NewBufferString(bad)), "invalid Unicode escape", "97", hint); err != nil {
		return err
	}
	if err := verify(second); err != nil {
		return err
	}
	r, err = first.QueryContext(ctx, "SELECT * FROM staged")
	if r != nil {
		r.Close()
		return fmt.Errorf("failed Restore published staged")
	}
	missingWant.Message = `relation "staged" does not exist`
	if !errors.As(err, &missing) || *missing != missingWant {
		return fmt.Errorf("staged lookup %#v %v", missing, err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, first, &dump); err != nil {
		return err
	}
	if strings.Contains(dump.String(), "U&") || !strings.Contains(dump.String(), `"A"`) || !strings.Contains(dump.String(), `"😀"`) {
		return fmt.Errorf("noncanonical Unicode dump: %s", dump.String())
	}
	if err := stuntgres.Reset(ctx, first); err != nil {
		return err
	}
	if err := stuntgres.Restore(ctx, first, bytes.NewReader(dump.Bytes())); err != nil {
		return err
	}
	for _, db := range []*sql.DB{first, second} {
		if err := verify(db); err != nil {
			return err
		}
	}
	if err := unicodeEscapesVerifyRows(held, []string{"id", "😀"}, []string{"INT4", "TEXT"}, want); err != nil {
		return err
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(ctx, second, &again); err != nil {
		return err
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		return fmt.Errorf("redump changed")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	s, err := first.PrepareContext(ctx, `INSERT INTO "A" VALUES (8,U&'\0041')`)
	if err != nil {
		return err
	}
	_, err = s.ExecContext(canceled)
	s.Close()
	if err := unicodeEscapesOrdinaryCanceled(err); err != nil {
		return err
	}
	if err := unicodeEscapesOrdinaryCanceled(stuntgres.Restore(canceled, first, bytes.NewReader(dump.Bytes()))); err != nil {
		return err
	}
	if err := verify(second); err != nil {
		return err
	}
	return nil
}
