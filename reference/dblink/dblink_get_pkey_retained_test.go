package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkGetPkeyRetained(t *testing.T) {
	pkeyCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE SCHEMA pk_left", "CREATE SCHEMA pk_right",
			"CREATE TABLE pk_left.pkey_retained(a integer,b integer,CONSTRAINT pkey_retained_pk PRIMARY KEY(b,a))",
			"CREATE TABLE pk_right.pkey_retained(other integer PRIMARY KEY)",
			"SET search_path=pk_left,public",
		} {
			run(fixture.Exec(q, 0))
		}
		held := pkeyHeld(t, p, c, pkeyCall)
		original := [][]any{{int64(1), "b"}, {int64(2), "a"}}
		pkeyRoutes(t, p, b, c, &held, pkeyRows(original, "pkey_retained"))
		var old *sql.Rows
		var columns []string
		var types []*sql.ColumnType
		if !b.PG {
			// Native Rows owns a detached materialization. pq instead retains
			// its live protocol cursor until drained.
			var err error
			old, err = c.QueryContext(p.Ctx, pkeyCall, "pkey_retained")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			columns, err = old.Columns()
			if err != nil {
				t.Fatal(err)
			}
			types, err = old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Exec("SET search_path=pk_right,public", 0))
		pkeyRoutes(t, p, b, c, &held, pkeyRows([][]any{{int64(1), "other"}}, "pkey_retained"))
		run(fixture.Exec("SET search_path=pk_left,public", 0))
		run(fixture.Exec(`ALTER TABLE pkey_retained RENAME COLUMN b TO "B current"`, 0))
		current := [][]any{{int64(1), "B current"}, {int64(2), "a"}}
		pkeyRoutes(t, p, b, c, &held, pkeyRows(current, "pkey_retained"))
		if old != nil {
			pkeyOldRows(t, old, columns, types, original)
		}
		for _, q := range []string{
			"CREATE VIEW pk_left.pkey_saved AS SELECT * FROM pk_ext.dblink_get_pkey('pk_left.pkey_retained')",
			"CREATE VIEW pk_left.pkey_value_saved AS SELECT k FROM pk_ext.dblink_get_pkey('pk_left.pkey_retained') AS k",
		} {
			run(fixture.Exec(q, 0))
		}
		const savedSQL = "SELECT * FROM pk_left.pkey_saved ORDER BY position"
		savedHeld := pkeyHeld(t, p, c, savedSQL)
		identity := func(routine string) [5]int64 {
			t.Helper()
			var ids [5]int64
			const q = "SELECT p.oid::bigint,t.oid::bigint,t.typarray::bigint,t.typrelid::bigint,e.oid::bigint FROM pg_proc p JOIN pg_type t ON t.oid=p.prorettype JOIN pg_depend d ON d.objid=p.oid AND d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid AND e.extname='dblink' WHERE p.oid=$1::regprocedure"
			if err := c.QueryRowContext(p.Ctx, q, routine).Scan(&ids[0], &ids[1], &ids[2], &ids[3], &ids[4]); err != nil {
				t.Fatal("live installed identity", err)
			}
			for _, id := range ids {
				if id <= 0 {
					t.Fatal("missing genuine nominal identity", ids)
				}
			}
			return ids
		}
		beforeMove := identity("pk_ext.dblink_get_pkey(text)")
		saved := fixture.Select(savedSQL, []string{"position", "colname"}, []string{"INT4", "TEXT"}, current)
		for _, change := range []struct{ name, sql string }{
			{"routine_rename", "ALTER FUNCTION pk_ext.dblink_get_pkey(text) RENAME TO retained_pkey"},
			{"routine_move", "ALTER FUNCTION pk_ext.retained_pkey(text) SET SCHEMA pk_right"},
			{"type_rename", "ALTER TYPE pk_ext.dblink_pkey_results RENAME TO retained_key"},
			{"type_move", "ALTER TYPE pk_ext.retained_key SET SCHEMA pk_right"},
		} {
			run(fixture.Exec(change.sql, 0))
			t.Run(change.name, func(t *testing.T) {
				// PREPARE's DDL invalidation reparses the original SQL text;
				// retained object identity does not make its old name an alias.
				// This independently expected complete record is also the
				// explicit pinned-oracle discovery for the same held statement.
				t.Run("original_name_held_reparse", func(t *testing.T) {
					s := pkeyError(pkeyCall, "42883", "function pk_ext.dblink_get_pkey(text) does not exist", "pk_left.pkey_retained")
					s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					s.Diagnostic.Position = fixture.Position(15)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				})
				// Stored definitions instead own the actual callable and type.
				pkeyRoutes(t, p, b, c, &savedHeld, saved)
			})
		}
		if got := identity("pk_right.retained_pkey(text)"); got != beforeMove {
			t.Fatal("rename/move substituted installed objects", got, beforeMove)
		}
		pkeyRoutes(t, p, b, c, &savedHeld, saved)
		const currentSQL = "SELECT * FROM pk_right.retained_pkey($1::text) ORDER BY position"
		currentHeld := pkeyHeld(t, p, c, currentSQL)
		pkeyRoutes(t, p, b, c, &currentHeld, fixture.Select(currentSQL, []string{"position", "colname"}, []string{"INT4", "TEXT"}, current, "pk_left.pkey_retained"))
		t.Run("obsolete_callable_spelling", func(t *testing.T) {
			pkeyAnalysisFailure(t, p, b, c, "SELECT * FROM pk_ext.dblink_get_pkey('pk_left.pkey_retained'::text)", "function pk_ext.dblink_get_pkey(text) does not exist")
		})
		q := "SELECT pg_get_viewdef('pk_left.pkey_saved'::regclass)::text LIKE '%pk_right.retained_pkey%' AS moved"
		pkeyRoutes(t, p, b, c, nil, fixture.Select(q, []string{"moved"}, []string{"BOOL"}, [][]any{{true}}))
		q = "SELECT pg_typeof(k)::oid='pk_right.retained_key'::regtype::oid AS nominal,k::text AS value FROM pk_left.pkey_value_saved ORDER BY (k).position"
		pkeyRoutes(t, p, b, c, nil, fixture.Select(q, []string{"nominal", "value"}, []string{"BOOL", "TEXT"}, [][]any{{true, `(1,"B current")`}, {true, "(2,a)"}}))
		// Execute the actual presented SQL, not a reconstructed equivalent.
		// The row view must expose both declared fields; the value view must
		// expose one genuine nominal composite with working field notation.
		for _, replay := range []struct {
			name, view, projection, order string
			columns, types                []string
			rows                          [][]any
		}{
			{"rows", "pk_left.pkey_saved", "position,colname", "position", []string{"position", "colname"}, []string{"INT4", "TEXT"}, current},
			{"nominal", "pk_left.pkey_value_saved", "pg_typeof(k)::oid='pk_right.retained_key'::regtype::oid AS nominal,k::text AS value", "(k).position", []string{"nominal", "value"}, []string{"BOOL", "TEXT"}, [][]any{{true, `(1,"B current")`}, {true, "(2,a)"}}},
		} {
			t.Run("deparsed_"+replay.name, func(t *testing.T) {
				var definition string
				if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef($1::regclass)", replay.view).Scan(&definition); err != nil {
					t.Fatal("view definition", err)
				}
				definition = strings.TrimSuffix(strings.TrimSpace(definition), ";")
				query := "SELECT " + replay.projection + " FROM (" + definition + ") AS deparsed ORDER BY " + replay.order
				pkeyRoutes(t, p, b, c, nil, fixture.Select(query, replay.columns, replay.types, replay.rows))
			})
		}
		run(fixture.Exec("ALTER TABLE pk_left.pkey_retained DROP CONSTRAINT pkey_retained_pk", 0))
		saved.Rows = nil
		pkeyRoutes(t, p, b, c, &savedHeld, saved)
		pkeyRoutes(t, p, b, c, &currentHeld, fixture.Select(currentSQL, []string{"position", "colname"}, []string{"INT4", "TEXT"}, nil, "pk_left.pkey_retained"))
		run(fixture.Exec("SET search_path=public", 0))
	})
}

func pkeyOldRows(t *testing.T, rows *sql.Rows, columns []string, types []*sql.ColumnType, want [][]any) {
	t.Helper()
	gotColumns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(gotColumns, columns) || !reflect.DeepEqual(columns, []string{"position", "colname"}) {
		t.Fatal("old columns changed", gotColumns, err)
	}
	gotTypes, err := rows.ColumnTypes()
	if err != nil || len(gotTypes) != 2 || len(types) != 2 {
		t.Fatal("old descriptor lost", gotTypes, err)
	}
	for i, name := range []string{"INT4", "TEXT"} {
		if gotTypes[i].DatabaseTypeName() != name || gotTypes[i].DatabaseTypeName() != types[i].DatabaseTypeName() || gotTypes[i].ScanType() != types[i].ScanType() {
			t.Fatal("old type generation changed", i)
		}
	}
	for _, expected := range want {
		if !rows.Next() {
			t.Fatal("old row missing", rows.Err())
		}
		var position int64
		var name string
		if err := rows.Scan(&position, &name); err != nil || position != expected[0] || name != expected[1] {
			t.Fatal("old value changed", position, name, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("old row tail", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
