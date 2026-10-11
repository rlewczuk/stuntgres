package fixture

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"github.com/ngrok/sqlmw"
	"reflect"
	"stuntgres"
	"testing"
)

func (m MigrationSmoke) AlterExec(db *sql.DB, q string, args ...any) {
	m.T.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		m.T.Fatal(q, err)
	}
}

func (m MigrationSmoke) AlterError(err error, code, message, position string) {
	m.T.Helper()
	want := Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
	AssertDiagnostic(m.T, err, &want)
}

func (m MigrationSmoke) NullDiagnostic(err error, e stuntgres.Error) {
	m.T.Helper()
	e.Severity, e.SeverityUnlocalized = "ERROR", "ERROR"
	want := MigrationDiagnostic(e)
	AssertDiagnostic(m.T, err, &want)
}

func (m MigrationSmoke) NullExec(db *sql.DB, prepared bool, q string, count int64, args ...any) {
	m.T.Helper()
	RunStep(m.T, m.T.Context(), db, m.Recorder, Step{Query: q, Prepared: prepared, NonContext: true, Affected: count, Args: args})
}

func (m MigrationSmoke) NullQuery(db *sql.DB, q string, names, types []string, nullable []bool, want [][]any) {
	m.T.Helper()
	rows, err := db.Query(q)
	if err != nil {
		m.T.Fatal(err)
	}
	m.NullRows(rows, names, types, nullable, want)
	m.RecordQuery(db, q, names, types, want)
}

func (m MigrationSmoke) DropRows(rows *sql.Rows, names, types []string, want [][]any) {
	m.NullRows(rows, names, types, nil, want)
}

func (m MigrationSmoke) NullRows(rows *sql.Rows, names, types []string, nullable []bool, want [][]any) {
	m.T.Helper()
	defer rows.Close()
	labels, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(labels, names) {
		m.T.Fatal(labels, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		m.T.Fatal(metadata, err)
	}
	for i, c := range metadata {
		if c.DatabaseTypeName() != types[i] {
			m.T.Fatal(c, types[i])
		}
		if !m.PG {
			scan := reflect.TypeFor[int64]()
			switch types[i] {
			case "TEXT", "VARCHAR", "NUMERIC":
				scan = reflect.TypeFor[string]()
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			case "FLOAT8":
				scan = reflect.TypeFor[float64]()
			}
			if c.ScanType() != scan {
				m.T.Fatal(c)
			}
			if nullable != nil {
				value, known := c.Nullable()
				if !known || value != nullable[i] {
					m.T.Fatal(c, value, known)
				}
			}
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		row := make([]any, len(names))
		targets := make([]any, len(names))
		for i := range row {
			targets[i] = &row[i]
		}
		if err := rows.Scan(targets...); err != nil {
			m.T.Fatal(err)
		}
		for i, v := range row {
			AssertRawValue(m.T, m.PG, types[i], v)
			row[i] = Normalize(v)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		m.T.Fatal(got, want, err)
	}
}

func MigrationAlterFixture(t *testing.T, p *Pair, backend Backend) (MigrationSmoke, *sql.DB, context.Context, *[]Diagnostic) {
	t.Helper()
	notices := new([]Diagnostic)
	db, c, recorder := NoticeReferencePool(t, p, backend.PG, notices)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	m := MigrationSmoke{T: t, PG: backend.PG, Native: p.Native, Recorder: recorder}
	return m, db, context.WithValue(p.Ctx, RecorderKey{}, recorder), notices
}

// Preserve non-Context consumers and independently check both recorded Context
// routes, including middleware-owned values and closure, on a read-only
// companion. Mutations are never replayed to manufacture recorder evidence.
func (m MigrationSmoke) RecordQuery(db *sql.DB, q string, names, types []string, want [][]any) {
	m.T.Helper()
	for _, prepared := range []bool{false, true} {
		RunStep(m.T, m.T.Context(), db, m.Recorder, Step{Query: q, Prepared: prepared, SelectRows: true, Columns: names, Types: types, Rows: want})
	}
}

func MigrationRecordedConnector(t *testing.T, c driver.Connector) driver.Connector {
	t.Helper()
	wrapped := sqlmw.Driver(NoticeReferenceDriver{c}, Interceptor{})
	connector, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	return connector
}
