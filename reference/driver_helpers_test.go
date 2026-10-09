package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
)

func driverExecSQL(t *testing.T, db *sql.DB, query string, args ...any) sql.Result {
	t.Helper()
	r, err := db.Exec(query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if _, err := r.LastInsertId(); err == nil {
		t.Fatal("driver exposed LastInsertId")
	}
	return r
}

func driverSQLState(t *testing.T, err error, want string) {
	t.Helper()
	referenceState(t, err, want)
}

func driverReadTuples(t *testing.T, db *sql.DB, query string, args ...any) [][]any {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	result := make([][]any, 0)
	for rows.Next() {
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = referenceNormalize(values[i])
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	return result
}

func driverWantTuples(t *testing.T, db *sql.DB, query string, want [][]any, args ...any) {
	t.Helper()
	got := driverReadTuples(t, db, query, args...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v want %#v", query, got, want)
	}
}

type driverTextValuer string

func (v driverTextValuer) Value() (driver.Value, error) { return string(v), nil }

func driverOrdinaryError(t *testing.T, err error, want error) {
	t.Helper()
	var state interface{ SQLState() string }
	if !errors.Is(err, want) || errors.As(err, &state) {
		t.Fatalf("ordinary error got %T %v want %v", err, err, want)
	}
}

func driverPool(t *testing.T, d *stuntgres.Driver, dsn string) *sql.DB {
	t.Helper()
	c, err := d.OpenConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func driverArgumentError(t *testing.T, err error) {
	t.Helper()
	var state interface{ SQLState() string }
	if err == nil || errors.As(err, &state) {
		t.Fatalf("adapter argument error got %T %v", err, err)
	}
}
