package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"
)

func TestReferenceDeleteReturningPreparedNullMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db, r := backend.db, backend.recorder
			referenceLockExec(t, p.ctx, db, "CREATE TABLE t(id INT NOT NULL,v TEXT,n NUMERIC(8,2),b BYTEA)", 0)
			q := "DELETE FROM t WHERE id=$1 RETURNING old.id,new.id,COALESCE(new.id,$1) AS kept,$2 AS label,n,b"
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, r)
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
					referenceLockExec(t, p.ctx, db, frame.insert, 1)
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
					if !backend.pg && (!k || n != want) || backend.pg && (n || k) {
						rows.Close()
						t.Fatal(i, n, k, want)
					}
				}
				if precision, scale, known := meta[4].DecimalSize(); !known || precision != 8 || scale != 2 {
					rows.Close()
					t.Fatal(precision, scale, known)
				}
				s := referenceSelect(q, []string{"id", "id", "kept", "label", "n", "b"}, []string{"INT4", "INT4", "INT4", "TEXT", "NUMERIC", "BYTEA"}, frame.want)
				names, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(names, s.columns) {
					rows.Close()
					t.Fatal(names, err)
				}
				for i, m := range meta {
					if m.DatabaseTypeName() != s.types[i] {
						rows.Close()
						t.Fatal(i, m.DatabaseTypeName(), s.types[i])
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
						referenceAssertRawValue(t, backend.pg, s.types[i], v)
						values[i] = referenceNormalize(v)
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
				deleteReferenceRecorded(t, r, s)
			}
		})
	}
}

func TestReferenceDeleteReturningCurrentSchemasMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db, r := backend.db, backend.recorder
			referenceLockExec(t, p.ctx, db, "CREATE TABLE t(id INT)", 0)
			q := "DELETE FROM t RETURNING current_schemas(NULL) AS schemas"
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, r)
			held, err := db.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, prepared := range []bool{false, true} {
				referenceLockExec(t, p.ctx, db, "INSERT INTO t VALUES(1)", 1)
				for _, want := range [][][]any{{{nil}}, {}} {
					var x referenceExecutor = db
					if prepared {
						x = referenceHeldStatement{referenceExecutor: db, statement: held, query: q}
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
					if !backend.pg && (!k || !n) || backend.pg && (n || k) {
						rows.Close()
						t.Fatal(n, k)
					}
					if backend.pg {
						if meta[0].ScanType() != reflect.TypeFor[any]() {
							rows.Close()
							t.Fatal(meta[0].ScanType())
						}
						if length, known := meta[0].Length(); length != 0 || known {
							rows.Close()
							t.Fatal(length, known)
						}
					}
					s := referenceSelect(q, []string{"schemas"}, []string{"_NAME"}, want)
					deleteReferenceConsume(t, backend.pg, rows, nil, s)
					deleteReferenceRecorded(t, r, s)
				}
			}
		})
	}
}

func TestReferenceDeleteReturningCommentSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, backend := range p.backends() {
			t.Run(backend.name, func(t *testing.T) {
				db, r := backend.db, backend.recorder
				for _, s := range []referenceStep{referenceExec("CREATE TABLE t(id INT)", 0), referenceExec("INSERT INTO t VALUES(1)", 1), referenceExec("COMMENT ON TABLE t IS 'committed table'", 0), referenceExec("COMMENT ON COLUMN t.id IS 'committed column'", 0)} {
					referenceRunStep(t, p.ctx, db, r, s)
				}
				tx, err := db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				s := referenceSelect("DELETE FROM t RETURNING id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment", []string{"id", "column_comment", "table_comment"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "committed column", "committed table"}})
				s.prepared = prepared
				referenceRunStep(t, p.ctx, tx, r, s)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				s.query = "SELECT id,col_description('t'::regclass,1) AS column_comment,obj_description('t'::regclass,'pg_class') AS table_comment FROM t"
				s.prepared = false
				referenceRunStep(t, p.ctx, db, r, s)
			})
		}
	}
}

func TestReferenceDeleteReturningOrdinaryAliasColumns(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE old(old INT,new TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO old VALUES(7,'value')", 1))
	p.step(t, referenceSelect("DELETE FROM old RETURNING old.*,old,new,new.*", []string{"old", "new", "old", "new", "old", "new"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "TEXT"}, [][]any{{int64(7), "value", int64(7), "value", nil, nil}}))
	p.step(t, referenceExec("CREATE TABLE t()", 0))
	p.step(t, referenceExec("INSERT INTO t DEFAULT VALUES", 1))
	p.step(t, referenceSelect("DELETE FROM t RETURNING *,1 AS one", []string{"one"}, []string{"INT4"}, [][]any{{int64(1)}}))
}
