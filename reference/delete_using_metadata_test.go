package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteUsingMetadataPrepared(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(referenceExec("CREATE TABLE t(id INT NOT NULL,v INT)", 0))
		run(referenceExec("CREATE TABLE s(k INT,txt VARCHAR(7),amount NUMERIC(8,3))", 0))
		run(referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		run(referenceExec("INSERT INTO s VALUES(1,'one',12.345)", 1))
		q := "DELETE FROM t d USING s WHERE d.id=s.k AND d.id=$1 RETURNING d.*,s.*,old.*,new.*"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		run(deleteUsingTargets([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		step := referenceSelect(q, []string{"id", "v", "k", "txt", "amount", "id", "v", "id", "v"}, []string{"INT4", "INT4", "INT4", "VARCHAR", "NUMERIC", "INT4", "INT4", "INT4", "INT4"}, nil, nil)
		step.checkMetadata = func(t *testing.T, rows *sql.Rows) {
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for i, typ := range types {
				length, lengthOK := typ.Length()
				wantLength := int64(0)
				if i == 3 {
					wantLength = 7
				}
				if length != wantLength || lengthOK != (i == 3) {
					t.Fatalf("column %d length=(%d,%t), want (%d,%t)", i, length, lengthOK, wantLength, i == 3)
				}
				precision, scale, decimalOK := typ.DecimalSize()
				wantPrecision, wantScale := int64(0), int64(0)
				if i == 4 {
					wantPrecision, wantScale = 8, 3
				}
				if precision != wantPrecision || scale != wantScale || decimalOK != (i == 4) {
					t.Fatalf("column %d decimal=(%d,%d,%t)", i, precision, scale, decimalOK)
				}
				nullable, nullableOK := typ.Nullable()
				if b.pg {
					if nullable || nullableOK {
						t.Fatal("PG nullable metadata availability", i, nullable, nullableOK)
					}
				} else {
					wantNullable := i != 0 && i != 5
					if !nullableOK || nullable != wantNullable {
						t.Fatal("target/source/NEW nullable metadata", i, nullable, nullableOK, wantNullable)
					}
				}
			}
		}
		for _, arg := range []any{nil, int64(9), int64(2)} {
			step.args = []any{arg}
			referenceRunStep(t, p.ctx, held, b.recorder, step)
		}
		step.args = []any{int64(1)}
		step.rows = [][]any{{int64(1), int64(10), int64(1), "one", "12.345", int64(1), int64(10), nil, nil}}
		referenceRunStep(t, p.ctx, held, b.recorder, step)
		step.rows = nil
		referenceRunStep(t, p.ctx, held, b.recorder, step)
		run(referenceExec("INSERT INTO s VALUES(2,'two',23.456)", 1))
		step.args = []any{int64(2)}
		step.rows = [][]any{{int64(2), int64(20), int64(2), "two", "23.456", int64(2), int64(20), nil, nil}}
		referenceRunStep(t, p.ctx, held, b.recorder, step)
		run(deleteUsingTargets(nil))
		run(referenceSelect("SELECT k FROM s ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	})
}

func TestReferenceDeleteUsingReturningScopes(t *testing.T) {
	for _, tc := range []struct {
		name, query    string
		columns, types []string
		rows           [][]any
	}{
		{"bare_star", "DELETE FROM t d USING s WHERE d.id=s.k RETURNING *", []string{"id", "v", "k", "label"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(10), int64(1), "source"}}},
		{"masked_old", "DELETE FROM t d USING s old WHERE d.id=old.k RETURNING WITH(OLD AS o) o.*,old.*,new.*", []string{"id", "v", "k", "label", "id", "v"}, []string{"INT4", "INT4", "INT4", "TEXT", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(1), "source", nil, nil}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
				run(referenceExec("CREATE TABLE s(k INT,label TEXT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,10)", 1))
				run(referenceExec("INSERT INTO s VALUES(1,'source')", 1))
				run(referenceSelect(tc.query, tc.columns, tc.types, tc.rows))
				run(deleteUsingTargets(nil))
				run(referenceSelect("SELECT k,label FROM s", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "source"}}))
			})
		})
	}
}
