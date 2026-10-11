package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Migration adapters retain the original concrete direct/prepared routes.
func queryLockMigratedExecute(ctx context.Context, x fixture.Executor, q string, prepared bool, args ...any) (sql.Result, error) {
	if !prepared {
		return x.ExecContext(ctx, q, args...)
	}
	stmt, err := x.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	return stmt.ExecContext(ctx, args...)
}
func queryLockMigratedAffected(ctx context.Context, x fixture.Executor, q string, prepared bool, want int64, args ...any) error {
	result, err := queryLockMigratedExecute(ctx, x, q, prepared, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != want {
		return fmt.Errorf("%s affected %d want %d", q, n, want)
	}
	return nil
}
func queryLockMigratedTyped(ctx context.Context, x fixture.Executor, q string, prepared, pg bool, name, typ string, want ...int64) error {
	out := fixture.RowQuery(ctx, x, q, prepared)
	if out.Stmt != nil {
		defer out.Stmt.Close()
	}
	if out.Rows != nil {
		defer out.Rows.Close()
	}
	if out.Err != nil {
		return out.Err
	}
	if out.Rows == nil {
		return fmt.Errorf("query returned no cursor")
	}
	columns, err := out.Rows.Columns()
	if err != nil {
		return err
	}
	types, err := out.Rows.ColumnTypes()
	if err != nil {
		return err
	}
	scan := reflect.TypeFor[int64]()
	if pg && typ == "INT4" {
		scan = reflect.TypeFor[int32]()
	}
	if !reflect.DeepEqual(columns, []string{name}) || len(types) != 1 || types[0].DatabaseTypeName() != typ || types[0].ScanType() != scan {
		return fmt.Errorf("metadata %v %v", columns, types)
	}
	var got []int64
	for out.Rows.Next() {
		var raw any
		if err := out.Rows.Scan(&raw); err != nil {
			return err
		}
		n, ok := raw.(int64)
		if !ok {
			return fmt.Errorf("raw integer has type %T", raw)
		}
		got = append(got, n)
	}
	if err := out.Rows.Err(); err != nil {
		return err
	}
	if len(got) != len(want) {
		return fmt.Errorf("rows %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			return fmt.Errorf("rows %v want %v", got, want)
		}
	}
	return out.Rows.Close()
}
func queryLockMigratedBlocked(t *testing.T, ctx context.Context, x fixture.Executor, pg bool, q string, prepared bool) {
	t.Helper()
	if pg {
		fixture.LockExec(t, ctx, x, "SET lock_timeout='30ms'", 0)
		out := fixture.RowExecute(ctx, x, q, prepared)
		if out.Result != nil {
			t.Fatal("failed conflict published result")
		}
		got := fixture.ExtractDiagnostic(t, out.Err)
		// Tuple coordinates are physical oracle data, never fabricated by native.
		words := strings.Fields(q)
		action, relation := "locking", "t"
		if words[0] == "UPDATE" {
			action, relation = "updating", words[1]
		}
		if words[0] == "DELETE" {
			action, relation = "deleting", words[2]
		}
		pattern := `^while ` + action + ` tuple \([0-9]+,[1-9][0-9]*\) in relation "` + regexp.QuoteMeta(relation) + `"$`
		if !regexp.MustCompile(pattern).MatchString(got.Context) {
			t.Fatal("physical tuple wait context", got)
		}
		got.Context = ""
		want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
		if got != want {
			t.Fatal(got, want)
		}
		fixture.LockExec(t, ctx, x, "RESET lock_timeout", 0)
		return
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	out := fixture.RowExecute(bounded, x, q, prepared)
	if out.Result != nil || !errors.Is(out.Err, context.DeadlineExceeded) {
		t.Fatal(out.Result, out.Err)
	}
	var state interface{ SQLState() string }
	if errors.As(out.Err, &state) {
		t.Fatal("caller deadline fabricated SQLSTATE", out.Err)
	}
}
func queryLockMigratedRows(t *testing.T, rows *sql.Rows, err error, names, types []string, want [][]any) {
	t.Helper()
	if rows != nil {
		defer rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if rows == nil {
		t.Fatal("query returned no cursor")
	}
	columns, e := rows.Columns()
	if e != nil || !reflect.DeepEqual(columns, names) {
		t.Fatal(columns, names, e)
	}
	metadata, e := rows.ColumnTypes()
	if e != nil || len(metadata) != len(types) {
		t.Fatal(metadata, e)
	}
	for i, typ := range types {
		if metadata[i].DatabaseTypeName() != typ {
			t.Fatal(metadata[i], typ)
		}
	}
	got := [][]any{}
	for rows.Next() {
		v := make([]any, len(names))
		dst := make([]any, len(names))
		for i := range v {
			dst[i] = &v[i]
		}
		if e := rows.Scan(dst...); e != nil {
			t.Fatal(e)
		}
		got = append(got, v)
	}
	if e := rows.Err(); e != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, e)
	}
	if e := rows.Close(); e != nil {
		t.Fatal(e)
	}
}
