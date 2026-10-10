package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func queryWindowRangeRetained(t *testing.T, rows *sql.Rows, names, types []string, want [][]any) {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil || len(cols) != len(names) {
		t.Fatal(cols, err)
	}
	for i, c := range cols {
		if c.Name() != names[i] || c.DatabaseTypeName() != types[i] {
			t.Fatal("retained metadata", i, c)
		}
	}
	var got [][]any
	for rows.Next() {
		values := make([]any, len(names))
		destinations := make([]any, len(names))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			values[i] = referenceNormalize(v)
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := referenceCompareRows(got, want, false); !ok {
		t.Fatal(got, want)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func queryWindowRangeControls(t *testing.T, p *referencePair, steps []referenceStep) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil || dump.Len() == 0 {
		t.Fatal("actual nonempty Dump", err)
	}
	fresh := newReferenceFixture(t, false, true)
	if err := stuntgres.Restore(fresh.ctx, fresh.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		for _, prepared := range []bool{false, true} {
			s.prepared = prepared
			fresh.step(t, s)
		}
	}
	held, err := fresh.memory.QueryContext(fresh.ctx, steps[0].query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	fresh.step(t, referenceExec("CREATE TABLE range_sentinel(v INT)", 0))
	fresh.step(t, referenceExec("INSERT INTO range_sentinel VALUES(91)", 1))
	var before bytes.Buffer
	if err := stuntgres.Dump(fresh.ctx, fresh.native, &before); err != nil {
		t.Fatal(err)
	}
	bad := append(append([]byte{}, dump.Bytes()...), []byte("\nSELECT broken_restore;\n")...)
	if err := stuntgres.Restore(fresh.ctx, fresh.native, bytes.NewReader(bad)); err == nil {
		t.Fatal("invalid Restore succeeded")
	}
	ctx, cancel := context.WithCancel(fresh.ctx)
	defer cancel()
	reader := &enumCancelReader{Reader: bytes.NewReader(dump.Bytes()), cancel: cancel}
	err = stuntgres.Restore(ctx, fresh.native, reader)
	var diagnostic interface{ SQLState() string }
	if reader.Len() != 0 || !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) {
		t.Fatal(reader.Len(), err)
	}
	var after bytes.Buffer
	if err := stuntgres.Dump(fresh.ctx, fresh.native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("failed/canceled Restore changed catalog", err)
	}
	fresh.step(t, referenceSelect("SELECT v FROM range_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
	if err := stuntgres.Reset(fresh.ctx, fresh.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(fresh.ctx, fresh.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	queryWindowRangeRetained(t, held, steps[0].columns, steps[0].types, steps[0].rows)
	for _, s := range steps {
		fresh.step(t, s)
	}
}

func TestReferenceQueryWindowRangeViews(t *testing.T) {
	queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowRangeSeed(t, p, b)
		for _, s := range []referenceStep{
			referenceExec("CREATE DOMAIN range_offset AS INTEGER", 0),
			referenceExec("CREATE TABLE offset_source(v range_offset)", 0), referenceExec("INSERT INTO offset_source VALUES(1)", 1),
			referenceExec("CREATE VIEW range_inline AS SELECT id,count(*) OVER(ORDER BY k RANGE 1::BIGINT PRECEDING) AS n FROM range_t", 0),
			referenceExec("CREATE VIEW range_named AS SELECT id,count(*) OVER w AS n FROM range_t WINDOW w AS(ORDER BY k,k DESC RANGE '1' PRECEDING),unused AS(ORDER BY k RANGE (SELECT v::INTEGER FROM offset_source) PRECEDING)", 0),
			referenceExec("CREATE VIEW range_subquery AS SELECT id,count(*) OVER(ORDER BY k RANGE (SELECT v::INTEGER FROM offset_source) PRECEDING) AS n FROM range_t", 0),
			referenceExec("CREATE VIEW range_numeric AS SELECT id,count(*) OVER w AS n FROM range_t WINDOW w AS(ORDER BY k::NUMERIC RANGE 1.00::NUMERIC PRECEDING)", 0),
			referenceExec("CREATE VIEW range_interval AS SELECT id,count(*) OVER(ORDER BY DATE '2025-01-01'+k RANGE INTERVAL '1 day' PRECEDING) AS n FROM range_t", 0),
			referenceExec("CREATE VIEW range_inline_numeric AS SELECT id,count(*) OVER(ORDER BY k::NUMERIC RANGE 1.00::NUMERIC PRECEDING) AS n FROM range_t", 0),
			referenceExec("CREATE VIEW range_named_interval AS SELECT id,count(*) OVER w AS n FROM range_t WINDOW w AS(ORDER BY DATE '2025-01-01'+k RANGE INTERVAL '1 day' PRECEDING)", 0),
			referenceExec("CREATE VIEW range_domain_literal AS SELECT id,count(*) OVER w AS n FROM range_t WINDOW w AS(ORDER BY k RANGE '1'::range_offset::INTEGER PRECEDING),unused AS(ORDER BY k RANGE '2'::range_offset::INTEGER PRECEDING)", 0),
			referenceExec("CREATE VIEW range_dupe_inline AS SELECT id,count(*) OVER(ORDER BY k+0.000::NUMERIC,k+0.000::NUMERIC DESC RANGE BETWEEN 1.00::NUMERIC PRECEDING AND 0.00::NUMERIC FOLLOWING) AS n FROM range_t", 0),
			referenceExec("CREATE VIEW range_dupe_named AS SELECT id,count(*) OVER w AS n FROM range_t WINDOW w AS(ORDER BY k+0.000::NUMERIC,k+0.000::NUMERIC DESC RANGE BETWEEN 1.00::NUMERIC PRECEDING AND 0.00::NUMERIC FOLLOWING)", 0),
			referenceExec("CREATE VIEW range_dupe_unused AS SELECT id,count(*) OVER w AS n FROM range_t WINDOW w AS(ORDER BY k+0.000::NUMERIC,k+0.000::NUMERIC DESC RANGE 1.00::NUMERIC PRECEDING),unused AS(ORDER BY k+0.000::NUMERIC,k+0.000::NUMERIC DESC RANGE BETWEEN CURRENT ROW AND 2.00::NUMERIC FOLLOWING)", 0),
			referenceExec("CREATE VIEW range_collision(k,id) AS SELECT count(*) OVER(ORDER BY k RANGE 1 PRECEDING),id FROM range_t", 0),
			referenceExec("ALTER TABLE range_t RENAME COLUMN k TO renamed", 0),
		} {
			queryWindowRun(t, p, b, s)
		}
		var steps []referenceStep
		for _, name := range []string{"range_inline", "range_named", "range_subquery", "range_numeric", "range_interval", "range_inline_numeric", "range_named_interval", "range_domain_literal", "range_dupe_inline", "range_dupe_named", "range_dupe_unused"} {
			s := queryWindowRangeResult("SELECT id,n FROM "+name+" ORDER BY id", 1, 3, 3, 1, 2, 2, 2)
			queryWindowRoutes(t, p, b, s)
			steps = append(steps, queryWindowReviewReplay(t, p, b, name, s)...)
		}
		collision := referenceSelect("SELECT k,id FROM range_collision ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(3), int64(2)}, {int64(3), int64(3)}, {int64(1), int64(4)}, {int64(2), int64(5)}, {int64(2), int64(6)}, {int64(2), int64(7)}})
		queryWindowRoutes(t, p, b, collision)
		steps = append(steps, collision)
		if !b.pg {
			queryWindowRangeControls(t, p, steps)
		}
	})
}

func TestReferenceQueryWindowRangeMigratedPersistentOwnership(t *testing.T) {
	queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE t(id INT,k INT)", 0), referenceExec("INSERT INTO t VALUES(1,2),(2,1)", 2),
			referenceExec("CREATE VIEW w(k,id) AS SELECT row_number() OVER ordered,id FROM t WINDOW base AS(),ordered AS(base ORDER BY k)", 0),
		} {
			queryWindowRun(t, p, b, s)
		}
		expected := referenceSelect("SELECT k,id FROM w ORDER BY id", []string{"k", "id"}, []string{"INT8", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}})
		queryWindowRoutes(t, p, b, expected)
		if !b.pg {
			queryWindowRangeControls(t, p, []referenceStep{expected})
		}
		queryWindowRun(t, p, b, referenceExec("ALTER TABLE t RENAME COLUMN k TO renamed", 0))
		queryWindowRoutes(t, p, b, expected)
	})
}

func TestReferenceQueryWindowRangeMigratedOverlayOwnership(t *testing.T) {
	queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowRun(t, p, b, referenceExec("CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)", 0))
		queryWindowRun(t, p, b, referenceExec("INSERT INTO t VALUES(1,1,TRUE),(2,NULL,NULL),(3,2,FALSE),(4,1,TRUE)", 4))
		expected := referenceSelect("SELECT id,row_number() OVER(ORDER BY id) AS rn,count(*) OVER(ORDER BY v) AS n FROM t ORDER BY id", []string{"id", "rn", "n"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(2)}, {int64(2), int64(2), int64(4)}, {int64(3), int64(3), int64(3)}, {int64(4), int64(4), int64(2)}})
		queryWindowRoutes(t, p, b, expected)
		for _, prepared := range []bool{false, true} {
			rows, closeStatement, err := transactionQuery(p.ctx, b.db, expected.query, prepared)
			if err != nil {
				t.Fatal(err)
			}
			queryWindowRun(t, p, b, referenceExec("INSERT INTO t VALUES(5,9,FALSE)", 1))
			if !b.pg {
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
			}
			queryWindowRangeRetained(t, rows, expected.columns, expected.types, expected.rows)
			if err := closeStatement(); err != nil {
				t.Fatal(err)
			}
			// Restore original fixture for the second route after native Reset/PG append.
			if b.pg {
				queryWindowRun(t, p, b, referenceExec("DELETE FROM t WHERE id=5", 1))
			} else {
				queryWindowRun(t, p, b, referenceExec("CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)", 0))
				queryWindowRun(t, p, b, referenceExec("INSERT INTO t VALUES(1,1,TRUE),(2,NULL,NULL),(3,2,FALSE),(4,1,TRUE)", 4))
			}
		}
	})
}
