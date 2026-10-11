package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

type oidReferenceValuer struct{ value driver.Value }

func (v oidReferenceValuer) Value() (driver.Value, error) { return v.value, nil }

func TestReferenceOIDBindFramesAndOwnership(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE o(id INT,v OID,w OID)", 0))
	for _, backend := range p.Backends() {
		func() {
			q := "INSERT INTO o VALUES($1,$2,$2)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			for i, input := range []any{"0", []byte("2147483648"), int32(-1), int64(-1), uint64(4294967295), oidReferenceValuer{"1"}, nil} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, i, input))
			}
			for i, input := range []any{"0", "4294967295", nil} {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 1, i+10, input))
			}
			owned := []byte("4294967295")
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO o VALUES(20,$1,$1)", 1, owned))
			owned[0] = '0'
			for i, value := range []any{int64(0), int64(2147483648), int64(4294967295), int64(4294967295), int64(4294967295), int64(1), nil} {
				fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, fixture.Select("SELECT v,w FROM o WHERE id=$1", []string{"v", "w"}, []string{"OID", "OID"}, [][]any{{value, value}}, i))
			}
			fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, fixture.Select("SELECT v FROM o WHERE id=20", []string{"v"}, []string{"OID"}, [][]any{{int64(4294967295)}}))
			query := "SELECT v FROM o WHERE v=$1 ORDER BY id"
			filter, err := backend.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer filter.Close()
			reused := fixture.HeldStatement{Executor: backend.DB, Statement: filter, SQL: query}
			for _, tc := range []struct {
				arg   any
				value any
				count int
			}{{"0", int64(0), 2}, {"4294967295", int64(4294967295), 5}, {nil, nil, 0}} {
				want := make([][]any, tc.count)
				for i := range want {
					want[i] = []any{tc.value}
				}
				fixture.OidReferenceRows(t, p.Ctx, backend, reused, fixture.Select(query, []string{"v"}, []string{"OID"}, want, tc.arg))
			}
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			result, err := stmt.ExecContext(ctx, 30, uint64(math.MaxInt64)+1)
			if backend.PG {
				if result != nil {
					t.Fatal("failed uint64 OID bind exposed a result", result)
				}
				fixture.State(t, err, "22003")
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{
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
			fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, fixture.Select("SELECT v,w FROM o WHERE id=30", []string{"v", "w"}, []string{"OID", "OID"}, nil))
			conflict := fixture.ImplicitError("SELECT $1 FROM o WHERE v=$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "oid versus text", Position: "8"})
			conflict.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(conflict))
		}()
	}
}

func TestReferenceOIDScanBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE o(id INT,v OID)", 0))
	p.Step(t, fixture.Exec("INSERT INTO o VALUES(20,'4294967295'),(6,NULL)", 2))
	for _, backend := range p.Backends() {
		ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
		for _, tc := range []struct {
			q    string
			want [][]any
		}{{"SELECT v FROM o WHERE id=20", [][]any{{int64(4294967295)}}}, {"SELECT v FROM o WHERE id=6", [][]any{{nil}}}, {"SELECT v FROM o WHERE id=999", nil}} {
			fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, fixture.Select(tc.q, []string{"v"}, []string{"OID"}, tc.want))
			rows, err := backend.DB.QueryContext(ctx, tc.q)
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
			if err := backend.DB.QueryRow("SELECT v FROM o WHERE id=20").Scan(destination); err != nil {
				t.Fatal(err)
			}
			if err := backend.DB.QueryRowContext(ctx, "SELECT v FROM o WHERE id=20").Scan(destination); err != nil {
				t.Fatal(err)
			}
		}
		var nonnullable int64
		err := backend.DB.QueryRowContext(ctx, "SELECT v FROM o WHERE id=6").Scan(&nonnullable)
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) {
			t.Fatal("ordinary NULL scan error", err)
		}
		err = backend.DB.QueryRow("SELECT v FROM o WHERE id=6").Scan(&nonnullable)
		if err == nil || errors.As(err, &state) {
			t.Fatal("ordinary non-Context NULL scan error", err)
		}
	}
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		for _, q := range []string{"SELECT v FROM o WHERE id=20", "SELECT v FROM o WHERE id=6", "SELECT v FROM o WHERE id=999"} {
			cursor, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, q, nil)
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
