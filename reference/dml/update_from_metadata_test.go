package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceUpdateFromMetadata(t *testing.T) {
	updateFromReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("CREATE TABLE s(k BIGINT,n INT,txt VARCHAR(7),amount NUMERIC(8,3),day DATE,stamp TIMESTAMP,flag BOOL)", 0))
		run(fixture.Exec("INSERT INTO s VALUES(1,101,NULL,12.345,DATE '2020-01-02',TIMESTAMP '2020-01-02 03:04:05',TRUE)", 1))
		run(fixture.Exec("CREATE TABLE r(k INT)", 0))
		run(fixture.Exec("INSERT INTO r VALUES(1)", 1))
		for _, predicate := range []string{"x.id=u.k", "FALSE"} {
			q := "UPDATE t x SET v=s.n FROM s JOIN r USING(k) AS u WHERE " + predicate + " RETURNING x.*,s.*,old.*,new.*,u.k"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				stmt.Close()
				t.Fatal(err)
			}
			defer rows.Close()
			cols, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			expected := []string{"id", "v", "k", "n", "txt", "amount", "day", "stamp", "flag", "id", "v", "id", "v", "k"}
			if len(cols) != len(expected) {
				t.Fatal(cols)
			}
			for i := range cols {
				if cols[i] != expected[i] {
					t.Fatal(cols)
				}
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			names := []string{"INT4", "INT4", "INT8", "INT4", "VARCHAR", "NUMERIC", "DATE", "TIMESTAMP", "BOOL", "INT4", "INT4", "INT4", "INT4", "INT8"}
			for i, typ := range types {
				if typ.DatabaseTypeName() != names[i] {
					t.Fatal(i, typ.DatabaseTypeName())
				}
				length, lok := typ.Length()
				precision, scale, pok := typ.DecimalSize()
				wantLength := int64(0)
				if i == 4 {
					wantLength = 7
				}
				if length != wantLength || lok != (i == 4) {
					t.Fatal("source length metadata", i, length, lok)
				}
				wantPrecision, wantScale := int64(0), int64(0)
				if i == 5 {
					wantPrecision, wantScale = 8, 3
				}
				if precision != wantPrecision || scale != wantScale || pok != (i == 5) {
					t.Fatal("source decimal metadata", i, precision, scale, pok)
				}
				if b.PG {
					nullable, nok := typ.Nullable()
					if nullable || nok {
						t.Fatal("PostgreSQL nullable availability", i, nullable, nok)
					}
					scanNames := []string{"int32", "int32", "int64", "int32", "string", "interface {}", "time.Time", "time.Time", "bool", "int32", "int32", "int32", "int32", "int64"}
					if typ.ScanType().String() != scanNames[i] {
						t.Fatal("PostgreSQL scan metadata", i, typ.ScanType())
					}
				}
			}
			count := 0
			for rows.Next() {
				var id, v, k, n, oldID, oldV, newID, newV, merged int64
				var txt sql.NullString
				var amount string
				var day, stamp any
				var flag bool
				if err := rows.Scan(&id, &v, &k, &n, &txt, &amount, &day, &stamp, &flag, &oldID, &oldV, &newID, &newV, &merged); err != nil {
					t.Fatal(err)
				}
				if id != 1 || v != 101 || k != 1 || n != 101 || txt.Valid || amount != "12.345" || !flag || oldID != 1 || oldV != 10 || newID != 1 || newV != 101 || merged != 1 {
					t.Fatal(id, v, k, n, txt, amount, oldID, oldV, newID, newV, merged)
				}
				fixture.DateRaw(t, b.PG, day)
				fixture.AssertRawValue(t, b.PG, "TIMESTAMP", stamp)
				timestamp, stampOK := fixture.Normalize(stamp).(time.Time)
				if !reflect.DeepEqual(fixture.Normalize(day), fixture.DateExpected(b.PG, "2020-01-02")) || !stampOK || !timestamp.Equal(time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)) {
					t.Fatal("source temporal values", day, stamp)
				}
				count++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			stmt.Close()
			wantCount := 1
			if predicate == "FALSE" {
				wantCount = 0
			}
			if count != wantCount {
				t.Fatal(count)
			}
		}
	})
}

func TestReferenceUpdateFromAdmissionRefresh(t *testing.T) {
	updateFromReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		updateFromSeed(run)
		run(fixture.Exec("CREATE TABLE r(k INT,z INT)", 0))
		run(fixture.Exec("INSERT INTO r VALUES(1,9),(3,8)", 2))
		holder, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback()
		updateFromRun(t, p, b, holder)(fixture.Exec("ALTER TABLE r ALTER COLUMN z SET NOT NULL", 0))
		pid := 0
		if b.PG {
			pid = fixture.LockPID(t, p.Ctx, c)
		}
		q := "UPDATE t x SET v=s.n+r.z FROM s JOIN r USING(k) WHERE x.id=s.k"
		join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			result, err := c.ExecContext(ctx, q)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		if b.PG {
			fixture.LockWait(t, p.Ctx, pid)
		}
		updateFromRun(t, p, b, b.DB)(fixture.Exec("COMMENT ON TABLE s IS 'compatible refresh'", 0))
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		o := join()
		if o.Err != nil {
			t.Fatal(o.Err)
		}
		fixture.LockAffected(t, o.Result, 2)
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(110)}, {int64(2), int64(20)}, {int64(3), int64(311)}, {int64(4), int64(40)}}))
	})
}

func TestReferenceUpdateFromTypmodAtomicity(t *testing.T) {
	updateFromReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(id INT,v VARCHAR(3))", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'one'),(2,'two')", 2))
		run(fixture.Exec("CREATE TABLE s(k INT,n TEXT)", 0))
		run(fixture.Exec("INSERT INTO s VALUES(1,'ok'),(2,'toolong')", 2))
		q := "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k"
		want := fixture.ControlsReferenceDiagnostic("22001", "value too long for type character varying(3)")
		updateFromDiagnostic(t, p.Ctx, c, q, want, false)
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(1), "one"}, {int64(2), "two"}}))
	})
}
