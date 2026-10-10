package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"github.com/ngrok/sqlmw"
	"reflect"
	"stuntgres"
	"testing"
)

func (m migrationSmoke) alterExec(db *sql.DB, q string, args ...any) {
	m.t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		m.t.Fatal(q, err)
	}
}

func (m migrationSmoke) alterError(err error, code, message, position string) {
	m.t.Helper()
	want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
	referenceAssertDiagnostic(m.t, err, &want)
}

func (m migrationSmoke) nullDiagnostic(err error, e stuntgres.Error) {
	m.t.Helper()
	e.Severity, e.SeverityUnlocalized = "ERROR", "ERROR"
	want := migrationDiagnostic(e)
	referenceAssertDiagnostic(m.t, err, &want)
}

func (m migrationSmoke) nullExec(db *sql.DB, prepared bool, q string, count int64, args ...any) {
	m.t.Helper()
	referenceRunStep(m.t, m.t.Context(), db, m.recorder, referenceStep{query: q, prepared: prepared, nonContext: true, affected: count, args: args})
}

func (m migrationSmoke) nullQuery(db *sql.DB, q string, names, types []string, nullable []bool, want [][]any) {
	m.t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		m.t.Fatal(err)
	}
	m.nullRows(rows, names, types, nullable, want)
	m.recordQuery(db, q, names, types, want)
}

func (m migrationSmoke) dropRows(rows *sql.Rows, names, types []string, want [][]any) {
	m.nullRows(rows, names, types, nil, want)
}

func (m migrationSmoke) nullRows(rows *sql.Rows, names, types []string, nullable []bool, want [][]any) {
	m.t.Helper()
	defer rows.Close()
	labels, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(labels, names) {
		m.t.Fatal(labels, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		m.t.Fatal(metadata, err)
	}
	for i, c := range metadata {
		if c.DatabaseTypeName() != types[i] {
			m.t.Fatal(c, types[i])
		}
		if !m.pg {
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
				m.t.Fatal(c)
			}
			if nullable != nil {
				value, known := c.Nullable()
				if !known || value != nullable[i] {
					m.t.Fatal(c, value, known)
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
			m.t.Fatal(err)
		}
		for i, v := range row {
			referenceAssertRawValue(m.t, m.pg, types[i], v)
			row[i] = referenceNormalize(v)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		m.t.Fatal(got, want, err)
	}
}

func migrationAlterFixture(t *testing.T, p *referencePair, backend referenceBackend) (migrationSmoke, *sql.DB, context.Context, *[]referenceDiagnostic) {
	t.Helper()
	notices := new([]referenceDiagnostic)
	db, c, recorder := noticeReferencePool(t, p, backend.pg, notices)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	m := migrationSmoke{t: t, pg: backend.pg, native: p.native, recorder: recorder}
	return m, db, context.WithValue(p.ctx, referenceRecorderKey{}, recorder), notices
}

// Preserve non-Context consumers and independently check both recorded Context
// routes, including middleware-owned values and closure, on a read-only
// companion. Mutations are never replayed to manufacture recorder evidence.
func (m migrationSmoke) recordQuery(db *sql.DB, q string, names, types []string, want [][]any) {
	m.t.Helper()
	for _, prepared := range []bool{false, true} {
		referenceRunStep(m.t, m.t.Context(), db, m.recorder, referenceStep{query: q, prepared: prepared, selectRows: true, columns: names, types: types, rows: want})
	}
}

func migrationRecordedConnector(t *testing.T, c driver.Connector) driver.Connector {
	t.Helper()
	wrapped := sqlmw.Driver(noticeReferenceDriver{c}, referenceInterceptor{})
	connector, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	return connector
}
