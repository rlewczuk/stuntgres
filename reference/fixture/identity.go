package fixture

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func IdentityAlwaysDiagnostic() Diagnostic {
	return Diagnostic{Code: "428C9", Message: `cannot insert a non-DEFAULT value into column "id"`, Detail: `Column "id" is an identity column defined as GENERATED ALWAYS.`, Hint: "Use OVERRIDING SYSTEM VALUE to override."}
}

func IdentityConnection(t *testing.T, ctx context.Context, db *sql.DB) *sql.Conn {
	t.Helper()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func IdentityHeld(t *testing.T, ctx context.Context, c Executor, q string) HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(ctx, q)
	if err != nil {
		if s != nil {
			s.Close()
		}
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return HeldStatement{Executor: c, Statement: s, SQL: q}
}

func IdentityEmptyMetadata(t *testing.T, ctx context.Context, backend Backend, prepared bool, q string, labels, types []string, nullable []bool) {
	t.Helper()
	ctx = context.WithValue(ctx, RecorderKey{}, backend.Recorder)
	var executor Executor = backend.DB
	if prepared {
		held := IdentityHeld(t, ctx, backend.DB, q)
		defer held.Statement.Close()
		executor = held
	}
	rows, err := executor.QueryContext(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, labels) {
		t.Fatal(cols, err)
	}
	cts, err := rows.ColumnTypes()
	if err != nil || len(cts) != len(types) {
		t.Fatal(cts, err)
	}
	for i, ct := range cts {
		if ct.DatabaseTypeName() != types[i] {
			t.Fatal(ct.DatabaseTypeName(), types[i])
		}
		want := reflect.TypeOf(int64(0))
		if backend.PG {
			if types[i] == "INT2" {
				want = reflect.TypeOf(int16(0))
			}
			if types[i] == "INT4" {
				want = reflect.TypeOf(int32(0))
			}
		}
		if ct.ScanType() != want {
			t.Fatal(ct.ScanType(), want)
		}
		n, known := ct.Nullable()
		if backend.PG {
			if known {
				t.Fatal("pq Nullable must be unknown")
			}
		} else if !known || n != nullable[i] {
			t.Fatal(n, known, nullable[i])
		}
	}
	if rows.Next() {
		t.Fatal("empty metadata query returned a row")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	// Reuse the fixture's owned recorder and cursor-closure checks for the empty shape.
	s := Select(q, labels, types, nil)
	s.Prepared = prepared
	RunStep(t, ctx, backend.DB, backend.Recorder, s)
}
