package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"reflect"
	"testing"
)

type oidReferenceValuer struct{ value driver.Value }

func (v oidReferenceValuer) Value() (driver.Value, error) { return v.value, nil }

func TestReferenceOIDBindFramesAndOwnership(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE o(id INT,v OID,w OID)", 0))
	for _, backend := range p.backends() {
		func() {
			q := "INSERT INTO o VALUES($1,$2,$2)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			for i, input := range []any{"0", []byte("2147483648"), int32(-1), int64(-1), uint64(4294967295), oidReferenceValuer{"1"}, nil} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, i, input))
			}
			for i, input := range []any{"0", "4294967295", nil} {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 1, i+10, input))
			}
			owned := []byte("4294967295")
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO o VALUES(20,$1,$1)", 1, owned))
			owned[0] = '0'
			for i, value := range []any{int64(0), int64(2147483648), int64(4294967295), int64(4294967295), int64(4294967295), int64(1), nil} {
				oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect("SELECT v,w FROM o WHERE id=$1", []string{"v", "w"}, []string{"OID", "OID"}, [][]any{{value, value}}, i))
			}
			oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect("SELECT v FROM o WHERE id=20", []string{"v"}, []string{"OID"}, [][]any{{int64(4294967295)}}))
			query := "SELECT v FROM o WHERE v=$1 ORDER BY id"
			filter, err := backend.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer filter.Close()
			reused := referenceHeldStatement{referenceExecutor: backend.db, statement: filter, query: query}
			for _, tc := range []struct {
				arg   any
				value any
				count int
			}{{"0", int64(0), 2}, {"4294967295", int64(4294967295), 5}, {nil, nil, 0}} {
				want := make([][]any, tc.count)
				for i := range want {
					want[i] = []any{tc.value}
				}
				oidReferenceRows(t, p.ctx, backend, reused, referenceSelect(query, []string{"v"}, []string{"OID"}, want, tc.arg))
			}
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			result, err := stmt.ExecContext(ctx, 30, uint64(math.MaxInt64)+1)
			if backend.pg {
				if result != nil {
					t.Fatal("failed uint64 OID bind exposed a result", result)
				}
				referenceState(t, err, "22003")
				referenceAssertDiagnostic(t, err, &referenceDiagnostic{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003",
					Message: `value "9223372036854775808" is out of range for type oid`,
					Context: "unnamed portal parameter $2 = '...'",
				})
			} else {
				var state interface{ SQLState() string }
				if err == nil || errors.As(err, &state) {
					t.Fatal("ordinary uint64 converter error", err)
				}
			}
			oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect("SELECT v,w FROM o WHERE id=30", []string{"v", "w"}, []string{"OID", "OID"}, nil))
			conflict := referenceImplicitError("SELECT $1 FROM o WHERE v=$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "oid versus text", Position: "8"})
			conflict.errorStage = "prepare"
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(conflict))
		}()
	}
}

func TestReferenceOIDScanBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE o(id INT,v OID)", 0))
	p.step(t, referenceExec("INSERT INTO o VALUES(20,'4294967295'),(6,NULL)", 2))
	for _, backend := range p.backends() {
		ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
		for _, tc := range []struct {
			q    string
			want [][]any
		}{{"SELECT v FROM o WHERE id=20", [][]any{{int64(4294967295)}}}, {"SELECT v FROM o WHERE id=6", [][]any{{nil}}}, {"SELECT v FROM o WHERE id=999", nil}} {
			oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect(tc.q, []string{"v"}, []string{"OID"}, tc.want))
			rows, err := backend.db.QueryContext(ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var v sql.NullInt64
				if err := rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, destination := range []any{new(int64), new(string), new([]byte), new(any), new(sql.NullInt64)} {
			// QueryRow is the original scanner route; Context is its separate
			// recorded companion, not evidence for the non-Context operation.
			if err := backend.db.QueryRow("SELECT v FROM o WHERE id=20").Scan(destination); err != nil {
				t.Fatal(err)
			}
			if err := backend.db.QueryRowContext(ctx, "SELECT v FROM o WHERE id=20").Scan(destination); err != nil {
				t.Fatal(err)
			}
		}
		var nonnullable int64
		err := backend.db.QueryRowContext(ctx, "SELECT v FROM o WHERE id=6").Scan(&nonnullable)
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) {
			t.Fatal("ordinary NULL scan error", err)
		}
		err = backend.db.QueryRow("SELECT v FROM o WHERE id=6").Scan(&nonnullable)
		if err == nil || errors.As(err, &state) {
			t.Fatal("ordinary non-Context NULL scan error", err)
		}
	}
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		for _, q := range []string{"SELECT v FROM o WHERE id=20", "SELECT v FROM o WHERE id=6", "SELECT v FROM o WHERE id=999"} {
			cursor, err := raw.(driver.QueryerContext).QueryContext(p.ctx, q, nil)
			if err != nil {
				return err
			}
			defer cursor.Close()
			check := func() {
				if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "OID" || cursor.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[int64]() {
					t.Fatal("retained OID metadata")
				}
			}
			check()
			for {
				err := cursor.Next(make([]driver.Value, 1))
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
			}
			check()
			if err := cursor.Close(); err != nil {
				return err
			}
			check()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
