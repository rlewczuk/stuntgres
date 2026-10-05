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

func booleanDiagnosticsDiagnostic(err error, message, position string) error {
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: message, Position: position}
	var origin *stuntgres.Error
	for depth := range 3 {
		var got *stuntgres.Error
		var state interface{ SQLState() string }
		if !errors.As(err, &got) || *got != want || !errors.As(err, &state) || state.SQLState() != "22P02" {
			return fmt.Errorf("diagnostic = %#v (%v), want %#v", got, err, want)
		}
		if depth == 0 {
			origin = got
		} else if got != origin {
			return fmt.Errorf("wrapping changed originating error")
		}
		err = fmt.Errorf("wrapped: %w", err)
	}
	return nil
}

func booleanDiagnosticsValues(db *sql.DB, want [][]any) error {
	rows, err := db.Query("SELECT id,flag FROM b ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	if len(types) != 2 || types[1].DatabaseTypeName() != "BOOL" || types[1].ScanType() != reflect.TypeFor[bool]() {
		return fmt.Errorf("boolean metadata changed: %#v", types)
	}
	var got [][]any
	for rows.Next() {
		var id, flag any
		if err := rows.Scan(&id, &flag); err != nil {
			return err
		}
		got = append(got, []any{id, flag})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("rows = %#v, want %#v", got, want)
	}
	return nil
}

func booleanDiagnosticsRun() error {
	connector, err := stuntgres.NewDriver().OpenConnector("boolean-diagnostics-smoke")
	if err != nil {
		return err
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	if err := db.Ping(); err != nil {
		return err
	}
	if _, err := db.Exec("CREATE TABLE b(id INT,flag BOOL)"); err != nil {
		return err
	}
	const invalid = `invalid input syntax for type boolean: "o"`
	// Empty-table binding is independent of row iteration.
	rows, err := db.Query("SELECT id FROM b WHERE flag='o'")
	if rows != nil {
		rows.Close()
		return fmt.Errorf("invalid WHERE exposed rows")
	}
	if err := booleanDiagnosticsDiagnostic(err, invalid, "29"); err != nil {
		return err
	}
	if _, err := db.Exec("INSERT INTO b VALUES (99,TRUE)"); err != nil {
		return err
	}
	sentinel := [][]any{{int64(99), true}}
	for _, tc := range []struct{ query, position string }{
		{"SELECT id FROM b WHERE flag='o'", "29"},
		{"/*é*/ SELECT id FROM b WHERE flag='o'", "35"},
		{"INSERT INTO b VALUES (1,'o')", "25"},
		{"INSERT INTO b VALUES (1,TRUE),(2,'o')", "34"},
	} {
		for _, operation := range []string{"exec", "query", "prepare"} {
			var failure error
			switch operation {
			case "exec":
				result, err := db.Exec(tc.query)
				failure = err
				if result != nil {
					return fmt.Errorf("invalid literal exposed result")
				}
			case "query":
				rows, err := db.Query(tc.query)
				failure = err
				if rows != nil {
					rows.Close()
					return fmt.Errorf("invalid literal exposed rows")
				}
			case "prepare":
				stmt, err := db.PrepareContext(context.Background(), tc.query)
				failure = err
				if stmt != nil {
					stmt.Close()
					return fmt.Errorf("invalid literal prepared successfully")
				}
			}
			if err := booleanDiagnosticsDiagnostic(failure, invalid, tc.position); err != nil {
				return err
			}
			if err := booleanDiagnosticsValues(db, sentinel); err != nil {
				return err
			}
		}
	}
	for _, query := range []string{"INSERT INTO b VALUES (1,$1)", "SELECT id FROM b WHERE flag=$1", "INSERT INTO b VALUES (1,TRUE),(2,$1)"} {
		stmt, err := db.Prepare(query)
		if err != nil {
			return err
		}
		for _, input := range []struct{ value, message string }{{"o", invalid}, {"", `invalid input syntax for type boolean: ""`}} {
			for _, prepared := range []bool{false, true} {
				var failure error
				if strings.HasPrefix(query, "SELECT") {
					var rows *sql.Rows
					if prepared {
						rows, failure = stmt.Query(input.value)
					} else {
						rows, failure = db.Query(query, input.value)
					}
					if rows != nil {
						rows.Close()
						stmt.Close()
						return fmt.Errorf("invalid parameter exposed rows")
					}
				} else if prepared {
					_, failure = stmt.Exec(input.value)
				} else {
					_, failure = db.Exec(query, input.value)
				}
				if err := booleanDiagnosticsDiagnostic(failure, input.message, ""); err != nil {
					stmt.Close()
					return err
				}
				if err := booleanDiagnosticsValues(db, sentinel); err != nil {
					stmt.Close()
					return err
				}
			}
		}
		stmt.Close()
	}
	stmt, err := db.Prepare("INSERT INTO b VALUES ($1,$2)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(1, "o")
	if err := booleanDiagnosticsDiagnostic(err, invalid, ""); err != nil {
		return err
	}
	for i, value := range []any{true, false, nil, []byte("tr"), " \tOfF\n"} {
		if _, err := stmt.Exec(i+1, value); err != nil {
			return err
		}
	}
	if _, err := db.Exec("INSERT INTO b VALUES (6,TRUE),(7,FALSE),(8,NULL)"); err != nil {
		return err
	}
	want := [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(4), true}, {int64(5), false}, {int64(6), true}, {int64(7), false}, {int64(8), nil}, {int64(99), true}}
	if err := booleanDiagnosticsValues(db, want); err != nil {
		return err
	}
	cursor, err := db.Query("SELECT id,flag FROM b ORDER BY id")
	if err != nil {
		return err
	}
	defer cursor.Close()
	ctx := context.Background()
	err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(flag BOOL); INSERT INTO replacement VALUES (TRUE),('o');"))
	if err := booleanDiagnosticsDiagnostic(err, invalid, "77"); err != nil {
		return err
	}
	if err := booleanDiagnosticsValues(db, want); err != nil {
		return err
	}
	rows, err = db.Query("SELECT flag FROM replacement")
	if rows != nil {
		rows.Close()
	}
	var missing *stuntgres.Error
	if !errors.As(err, &missing) || missing.Code != "42P01" {
		return fmt.Errorf("failed restore published replacement: %v", err)
	}
	var dump, redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		return err
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		return err
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		return err
	}
	if err := booleanDiagnosticsValues(db, want); err != nil {
		return err
	}
	if err := stuntgres.Dump(ctx, db, &redump); err != nil {
		return err
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		return fmt.Errorf("boolean dump changed after restore")
	}
	for _, row := range want {
		if !cursor.Next() {
			return fmt.Errorf("retained cursor ended: %v", cursor.Err())
		}
		var id, flag any
		if err := cursor.Scan(&id, &flag); err != nil {
			return err
		}
		if id != row[0] || flag != row[1] {
			return fmt.Errorf("retained cursor changed")
		}
	}
	if cursor.Next() || cursor.Err() != nil {
		return fmt.Errorf("retained cursor tail: %v", cursor.Err())
	}
	return nil
}

func TestBooleanDiagnosticsSmoke(t *testing.T) {
	if err := booleanDiagnosticsRun(); err != nil {
		t.Fatal(err)
	}
}
