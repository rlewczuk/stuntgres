package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLCursorHoldScopeAndPresentation(t *testing.T) {
	t.Run("commit_warning_policy", holdCommitWarningPolicy)
	for _, first := range []string{"commit", "fetch", "fetch-zero"} {
		t.Run(first, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				var owner string
				if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
					t.Fatal(err)
				}
				boundary := func() time.Time {
					var stamp time.Time
					if err := c.QueryRowContext(p.Ctx, "SELECT statement_timestamp()").Scan(&stamp); err != nil {
						t.Fatal(err)
					}
					return stamp.Round(0).UTC()
				}
				holdRun(t, p, b, c, "BEGIN")
				holdRun(t, p, b, c, "DECLARE env NO SCROLL CURSOR WITH HOLD FOR SELECT i,current_user AS actor,statement_timestamp() AS stamp,'cursor_rows'::REGCLASS AS r,'integer'::REGTYPE AS typ FROM cursor_rows ORDER BY i")
				var stamp time.Time
				if first == "fetch" {
					before := boundary()
					holdCaptureScope(t, p, b, c, owner, 1, &stamp)
					after := boundary()
					holdStampWithin(t, stamp, before, after)
				} else if first == "fetch-zero" {
					s := fixture.Select("FETCH 0 FROM env", holdScopeNames, holdScopeTypes, [][]any{})
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { holdScopeMetadata(t, rows, b.PG, holdScopeTypes) }
					sqlCursorStep(t, p, b, c, s)
				}
				holdRun(t, p, b, c, "SET search_path=pg_catalog")
				before := boundary()
				holdRun(t, p, b, c, "COMMIT")
				after := boundary()
				holdRun(t, p, b, c, "SET search_path=public")
				if first == "fetch" {
					if stamp.After(before) {
						t.Fatal("first FETCH timestamp changed to later COMMIT demand", stamp, before)
					}
					holdScopeBatch(t, p, b, c, "FETCH 1 FROM env", [][]any{{int64(2), owner, stamp, "cursor_rows", "integer"}})
				} else {
					// No effective FETCH: COMMIT is first demand, including after
					// FETCH0. The timestamp must be inside the COMMIT brackets.
					holdCaptureScope(t, p, b, c, owner, 1, &stamp)
					holdStampWithin(t, stamp, before, after)
				}
				holdRun(t, p, b, c, "SET search_path=pg_catalog")
				want := [][]any{{int64(3), owner, stamp, "public.cursor_rows", "integer"}}
				if first != "fetch" {
					want = append([][]any{{int64(2), owner, stamp, "public.cursor_rows", "integer"}}, want...)
				}
				holdScopeBatch(t, p, b, c, "FETCH ALL FROM env", want)
				holdScopeBatch(t, p, b, c, "FETCH ALL FROM env", [][]any{})
			})
		})
	}
	t.Run("role_and_temporal", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			sqlCursorSetup(t, p, b, c)
			holdRun(t, p, b, c, "CREATE ROLE hold_actor")
			defer func() {
				c.ExecContext(p.Ctx, "ROLLBACK")
				c.ExecContext(p.Ctx, "RESET ROLE")
				c.ExecContext(p.Ctx, "REVOKE SELECT ON public.cursor_rows FROM hold_actor")
				c.ExecContext(p.Ctx, "DROP ROLE hold_actor")
			}()
			holdRun(t, p, b, c, "GRANT SELECT ON cursor_rows TO hold_actor")
			holdRun(t, p, b, c, "BEGIN")
			holdRun(t, p, b, c, "DECLARE env NO SCROLL CURSOR WITH HOLD FOR SELECT i,current_user AS actor,TIMESTAMPTZ '2020-01-02 03:04:05+00' AS stamp FROM cursor_rows ORDER BY i")
			holdRun(t, p, b, c, "SET ROLE hold_actor")
			holdRun(t, p, b, c, "SET TIME ZONE 'America/New_York'")
			holdRun(t, p, b, c, "COMMIT")
			holdRun(t, p, b, c, "RESET ROLE")
			holdRun(t, p, b, c, "SET TIME ZONE 'UTC'")
			stamp := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
			s := fixture.Select("FETCH ALL FROM env", []string{"i", "actor", "stamp"}, []string{"INT4", "NAME", "TIMESTAMPTZ"}, [][]any{{int64(1), "hold_actor", stamp}, {int64(2), "hold_actor", stamp}, {int64(3), "hold_actor", stamp}})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { holdScopeMetadata(t, rows, b.PG, s.Types) }
			sqlCursorStep(t, p, b, c, s)
		})
	})
}

var holdScopeNames = []string{"i", "actor", "stamp", "r", "typ"}
var holdScopeTypes = []string{"INT4", "NAME", "TIMESTAMPTZ", "REGCLASS", "REGTYPE"}

func holdStampWithin(t *testing.T, stamp, before, after time.Time) {
	t.Helper()
	if stamp.Before(before) || stamp.After(after) {
		t.Fatalf("first-demand timestamp %s outside [%s,%s]", stamp, before, after)
	}
}

func holdCaptureScope(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, owner string, id int64, stamp *time.Time) {
	t.Helper()
	s := fixture.Select("FETCH 1 FROM env", holdScopeNames, holdScopeTypes, [][]any{{id, owner, nil, "cursor_rows", "integer"}})
	s.CheckScan = func(t *testing.T, rows *sql.Rows, row int) {
		var gotID int64
		var actor, relation, typ string
		if err := rows.Scan(&gotID, &actor, stamp, &relation, &typ); err != nil {
			t.Fatal(err)
		}
		*stamp = stamp.Round(0).UTC()
		s.Rows[row][2] = *stamp
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { holdScopeMetadata(t, rows, b.PG, s.Types) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if s.Types[column] != "REGTYPE" {
			return false
		}
		fixture.RegtypeRaw(t, b.PG, value)
		return true
	}
	sqlCursorStep(t, p, b, c, s)
}

func holdScopeBatch(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, rows [][]any) {
	t.Helper()
	s := fixture.Select(query, holdScopeNames, holdScopeTypes, rows)
	s.CheckMetadata = func(t *testing.T, result *sql.Rows) { holdScopeMetadata(t, result, b.PG, s.Types) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if s.Types[column] != "REGTYPE" {
			return false
		}
		fixture.RegtypeRaw(t, b.PG, value)
		return true
	}
	sqlCursorStep(t, p, b, c, s)
}

func holdScopeMetadata(t *testing.T, rows *sql.Rows, pg bool, types []string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != len(types) {
		t.Fatal("scope descriptor width", len(columns), len(types))
	}
	for i, column := range columns {
		var scan reflect.Type
		switch types[i] {
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		case "TIMESTAMPTZ":
			scan = reflect.TypeFor[time.Time]()
		default:
			scan = reflect.TypeFor[string]()
			if pg {
				scan = reflect.TypeFor[any]()
			}
		}
		nullable, known := column.Nullable()
		length, lengthKnown := column.Length()
		precision, scale, decimalKnown := column.DecimalSize()
		if column.DatabaseTypeName() != types[i] || column.ScanType() != scan || length != 0 || lengthKnown || precision != 0 || scale != 0 || decimalKnown {
			t.Fatalf("scope metadata column%d: type=%s scan=%v length=%d/%v decimal=%d,%d/%v", i, column.DatabaseTypeName(), column.ScanType(), length, lengthKnown, precision, scale, decimalKnown)
		}
		if nullable || known != (!pg && i == 0) {
			t.Fatalf("scope nullable column%d: %v/%v", i, nullable, known)
		}
	}
}
