package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteReturningPreparedNullMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db, r := backend.DB, backend.Recorder
			fixture.LockExec(t, p.Ctx, db, "CREATE TABLE t(id INT NOT NULL,v TEXT,n NUMERIC(8,2),b BYTEA)", 0)
			q := "DELETE FROM t WHERE id=$1 RETURNING old.id,new.id,COALESCE(new.id,$1) AS kept,$2 AS label,n,b"
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, r)
			held, err := db.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, frame := range []struct {
				insert string
				args   []any
				want   [][]any
			}{
				{"INSERT INTO t VALUES(2,'a',12.30,'\\x0102')", []any{2, "fresh"}, [][]any{{int64(2), nil, int64(2), "fresh", "12.30", string([]byte{1, 2})}}},
				{"", []any{nil, nil}, [][]any{}},
				{"INSERT INTO t VALUES(3,'b',NULL,NULL)", []any{3, "next"}, [][]any{{int64(3), nil, int64(3), "next", nil, nil}}},
			} {
				if frame.insert != "" {
					fixture.LockExec(t, p.Ctx, db, frame.insert, 1)
				}
				rows, err := held.QueryContext(ctx, frame.args...)
				if err != nil {
					t.Fatal(err)
				}
				meta, err := rows.ColumnTypes()
				if err != nil || len(meta) != 6 {
					rows.Close()
					t.Fatal(meta, err)
				}
				for i, want := range []bool{false, true} {
					n, k := meta[i].Nullable()
					if !backend.PG && (!k || n != want) || backend.PG && (n || k) {
						rows.Close()
						t.Fatal(i, n, k, want)
					}
				}
				if precision, scale, known := meta[4].DecimalSize(); !known || precision != 8 || scale != 2 {
					rows.Close()
					t.Fatal(precision, scale, known)
				}
				s := fixture.Select(q, []string{"id", "id", "kept", "label", "n", "b"}, []string{"INT4", "INT4", "INT4", "TEXT", "NUMERIC", "BYTEA"}, frame.want)
				names, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(names, s.Columns) {
					rows.Close()
					t.Fatal(names, err)
				}
				for i, m := range meta {
					if m.DatabaseTypeName() != s.Types[i] {
						rows.Close()
						t.Fatal(i, m.DatabaseTypeName(), s.Types[i])
					}
				}
				for _, want := range frame.want {
					if !rows.Next() {
						rows.Close()
						t.Fatal("missing RETURNING row", rows.Err())
					}
					values := make([]any, 6)
					dest := make([]any, 6)
					for i := range values {
						dest[i] = &values[i]
					}
					if err := rows.Scan(dest...); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					if want[5] != nil {
						payload, ok := values[5].([]byte)
						if !ok || !bytes.Equal(payload, []byte{1, 2}) {
							rows.Close()
							t.Fatal("raw BYTEA", values[5])
						}
					}
					for i, v := range values {
						fixture.AssertRawValue(t, backend.PG, s.Types[i], v)
						values[i] = fixture.Normalize(v)
					}
					if !reflect.DeepEqual(values, want) {
						rows.Close()
						t.Fatal(values, want)
					}
				}
				if rows.Next() || rows.Err() != nil {
					rows.Close()
					t.Fatal(rows.Err())
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				fixture.DeleteReferenceRecorded(t, r, s)
			}
		})
	}
}

func TestReferenceDeleteReturningCurrentSchemasMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db, r := backend.DB, backend.Recorder
			fixture.LockExec(t, p.Ctx, db, "CREATE TABLE t(id INT)", 0)
			q := "DELETE FROM t RETURNING current_schemas(NULL) AS schemas"
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, r)
			held, err := db.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, prepared := range []bool{false, true} {
				fixture.LockExec(t, p.Ctx, db, "INSERT INTO t VALUES(1)", 1)
				for _, want := range [][][]any{{{nil}}, {}} {
					var x fixture.Executor = db
					if prepared {
						x = fixture.HeldStatement{Executor: db, Statement: held, SQL: q}
					}
					rows, err := x.QueryContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					meta, err := rows.ColumnTypes()
					if err != nil || len(meta) != 1 {
						rows.Close()
						t.Fatal(meta, err)
					}
					n, k := meta[0].Nullable()
					if !backend.PG && (!k || !n) || backend.PG && (n || k) {
						rows.Close()
						t.Fatal(n, k)
					}
					if backend.PG {
						if meta[0].ScanType() != reflect.TypeFor[any]() {
							rows.Close()
							t.Fatal(meta[0].ScanType())
						}
						if length, known := meta[0].Length(); length != 0 || known {
							rows.Close()
							t.Fatal(length, known)
						}
					}
					s := fixture.Select(q, []string{"schemas"}, []string{"_NAME"}, want)
					fixture.DeleteReferenceConsume(t, backend.PG, rows, nil, s)
					fixture.DeleteReferenceRecorded(t, r, s)
				}
			}
		})
	}
}

func TestReferenceDeleteReturningCommentSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				db, r := backend.DB, backend.Recorder
				for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT)", 0), fixture.Exec("INSERT INTO t VALUES(1)", 1), fixture.Exec("COMMENT ON TABLE t IS 'committed table'", 0), fixture.Exec("COMMENT ON COLUMN t.id IS 'committed column'", 0)} {
					fixture.RunStep(t, p.Ctx, db, r, s)
				}
				tx, err := db.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				s := fixture.Select("DELETE FROM t RETURNING id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment", []string{"id", "column_comment", "table_comment"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "committed column", "committed table"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, tx, r, s)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				s.Query = "SELECT id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment FROM t"
				s.Prepared = false
				fixture.RunStep(t, p.Ctx, db, r, s)
			})
		}
	}
}

func TestReferenceDeleteReturningOrdinaryAliasColumns(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE old(old INT,new TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO old VALUES(7,'value')", 1))
	p.Step(t, fixture.Select("DELETE FROM old RETURNING old.*,old,new,new.*", []string{"old", "new", "old", "new", "old", "new"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT"}, [][]any{{int64(7), "value", int64(7), "value", nil, nil}}))
	p.Step(t, fixture.Exec("CREATE TABLE t()", 0))
	p.Step(t, fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
	p.Step(t, fixture.Select("DELETE FROM t RETURNING *,1 AS one", []string{"one"}, []string{"INT4"}, [][]any{{int64(1)}}))
}
