package reference_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceFloatingArithmeticSmoke(t *testing.T) {
	referenceFloatingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceFloatingRun(t, p, b, s) }
		run(referenceExec("CREATE TABLE f(id INT,r REAL,d DOUBLE PRECISION,i INT)", 0))
		run(referenceExec("INSERT INTO f VALUES(1,'7.5','2',2),(2,NULL,NULL,NULL),(3,'16777216','1',1)", 3))
		for _, s := range []referenceStep{
			referenceSelect("SELECT r+r,r+i,2^3^2,2^(3^2) FROM f WHERE id=1", []string{"?column?", "?column?", "?column?", "?column?"}, []string{"FLOAT4", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{float64(15), float64(9.5), float64(64), float64(512)}}),
			referenceSelect("SELECT r+'1',r+i FROM f WHERE id=3", []string{"?column?", "?column?"}, []string{"FLOAT4", "FLOAT8"}, [][]any{{float64(16777216), float64(16777217)}}),
		} {
			run(s)
			run(referencePrepared(s))
		}
		for _, condition := range []string{"id=2", "FALSE"} {
			want := [][]any{}
			if condition == "id=2" {
				want = [][]any{{nil, nil, nil}}
			}
			step := referenceSelect("SELECT r+r,r+i,r^d FROM f WHERE "+condition, []string{"?column?", "?column?", "?column?"}, []string{"FLOAT4", "FLOAT8", "FLOAT8"}, want)
			for _, prepared := range []bool{false, true} {
				step.prepared = prepared
				referenceRunStep(t, p.ctx, referenceArithmeticMetadataExecutor{b.db, t, b.pg, step.types}, b.recorder, step)
			}
		}
		for _, tc := range []referenceScalarFloatCase{
			{"NaN", "SELECT 'NaN'::real/'0'::real AS result FROM f WHERE id=1", "FLOAT4", math.NaN(), 0},
			{"infinity", "SELECT 'Infinity'::double precision+'1'::double precision AS result FROM f WHERE id=1", "FLOAT8", math.Inf(1), 0},
			{"negative_zero", "SELECT '1'::real/'-Infinity'::real AS result FROM f WHERE id=1", "FLOAT4", math.Copysign(0, -1), 0},
		} {
			for _, prepared := range []bool{false, true} {
				referenceScalarFloatQuery(t, p.ctx, b.db, b.recorder, tc, prepared)
			}
		}
		referenceFloatingError(t, p, b, "SELECT '0'::double precision ^ '-1'::double precision FROM f WHERE id=1", "2201F", "zero raised to a negative power is undefined")
		referenceFloatingError(t, p, b, "SELECT '1e200'::double precision ^ '2'::double precision FROM f WHERE id=1", "22003", "value out of range: overflow")
		referenceFloatingError(t, p, b, "SELECT '1e-200'::double precision ^ '2'::double precision FROM f WHERE id=1", "22003", "value out of range: underflow")
		missing := referenceImplicitError("SELECT r % r FROM f", referenceDiagnostic{Code: "42883", Message: "operator does not exist: real % real", Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
		missing.selectRows = true
		run(missing)
		missing.errorStage = "prepare"
		run(referencePrepared(missing))
		run(referenceExec("CREATE TABLE destination(id INT,d DOUBLE PRECISION)", 0))
		run(referenceExec("INSERT INTO destination VALUES(99,'99')", 1))
		run(referenceExec("CREATE TABLE source(id INT,d DOUBLE PRECISION)", 0))
		run(referenceExec("INSERT INTO source VALUES(1,'2'),(2,'0')", 2))
		for _, q := range []string{"INSERT INTO destination SELECT id,10/d FROM source ORDER BY id", "INSERT INTO destination VALUES(1,'7'::real/'2'::real),(2,'1'::real/'0'::real)", "UPDATE source SET d=10/d"} {
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
				run(referenceSelect("SELECT id,d FROM destination ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(99), float64(99)}}))
				run(referenceSelect("SELECT id,d FROM source ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(2)}, {int64(2), float64(0)}}))
			}
		}
		tx, err := b.db.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		s := referenceImplicitError("UPDATE source SET d=10/d", referenceDiagnostic{Code: "22012", Message: "division by zero"})
		referenceRunStep(t, p.ctx, tx, b.recorder, s)
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceImplicitError("SELECT d FROM source", referenceTransactionAborted()))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if !b.pg {
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			if _, err := b.db.ExecContext(canceled, "INSERT INTO destination VALUES(1,2^3)"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if rows, err := b.db.QueryContext(canceled, "SELECT r+r FROM f"); !errors.Is(err, context.Canceled) || rows != nil {
				t.Fatal(rows, err)
			}
			if stmt, err := b.db.PrepareContext(canceled, "SELECT r+r FROM f"); !errors.Is(err, context.Canceled) || stmt != nil {
				t.Fatal(stmt, err)
			}
		}
	})
}

func TestReferenceFloatingArithmeticControlsSmoke(t *testing.T) {
	referenceFloatingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceFloatingRun(t, p, b, s) }
		run(referenceExec("CREATE TABLE f(id INT,r REAL DEFAULT ('16777216'::real+'1'::real),d DOUBLE PRECISION DEFAULT ('16777216'::real+1),p DOUBLE PRECISION DEFAULT (2^3^2),q DOUBLE PRECISION DEFAULT (2^(3^2)),CHECK(p=64),CONSTRAINT f_float_check CHECK(r+d>0))", 0))
		run(referenceExec("INSERT INTO f(id) VALUES(1)", 1))
		expected := referenceSelect("SELECT r,d,p,q FROM f", []string{"r", "d", "p", "q"}, []string{"FLOAT4", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{float64(16777216), float64(16777217), float64(64), float64(512)}})
		rejectArithmeticCheck := func(ids [][]any) {
			for _, prepared := range []bool{false, true} {
				step := referenceImplicitError("INSERT INTO f VALUES(9,'-2','1',64,512)", referenceCheckViolation("f", "f_float_check", "9, -2, 1, 64, 512"))
				step.prepared, step.errorStage = prepared, "execute"
				run(step)
				run(referenceSelect("SELECT id FROM f ORDER BY id", []string{"id"}, []string{"INT4"}, ids))
				run(expected)
			}
		}
		rejectArithmeticCheck([][]any{{int64(1)}})
		run(referenceExec("CREATE VIEW v AS SELECT r+r AS r,d+1 AS d,p^1 AS p,q^1 AS q FROM f", 0))
		run(expected)
		run(referencePrepared(expected))
		view := referenceSelect("SELECT r,d,p,q FROM v", []string{"r", "d", "p", "q"}, []string{"FLOAT4", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{float64(33554432), float64(16777218), float64(64), float64(512)}})
		run(view)
		run(expected)
		if b.pg {
			return
		}
		db := p.native
		held, err := db.QueryContext(p.ctx, "SELECT r+r AS computed FROM f")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		meta, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var dump, redump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, db, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, db); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Dump(p.ctx, db, &redump); err != nil || dump.String() != redump.String() {
			t.Fatal(dump.String(), redump.String(), err)
		}
		run(expected)
		run(view)
		// Materialized pre-Dump values cannot prove restored expression typing.
		// A fresh omitted-column insert must execute the restored defaults and CHECK.
		run(referenceExec("INSERT INTO f(id) VALUES(2)", 1))
		fresh := referenceSelect("SELECT r,d,p,q FROM f WHERE id=2", []string{"r", "d", "p", "q"}, []string{"FLOAT4", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{float64(16777216), float64(16777217), float64(64), float64(512)}})
		for _, prepared := range []bool{false, true} {
			fresh.prepared = prepared
			run(fresh)
			computed := referenceSelect("SELECT r+0::real,d+0::double precision,p^1,q^1 FROM f WHERE id=2", []string{"?column?", "?column?", "?column?", "?column?"}, []string{"FLOAT4", "FLOAT8", "FLOAT8", "FLOAT8"}, fresh.rows)
			computed.prepared = prepared
			referenceRunStep(t, p.ctx, referenceArithmeticMetadataExecutor{b.db, t, b.pg, computed.types}, b.recorder, computed)
		}
		expected.rows = append(expected.rows, []any{float64(16777216), float64(16777217), float64(64), float64(512)})
		view.rows = append(view.rows, []any{float64(33554432), float64(16777218), float64(64), float64(512)})
		rejectArithmeticCheck([][]any{{int64(1)}, {int64(2)}})
		run(expected)
		run(view)
		for _, script := range []string{"CREATE TABLE replacement(d DOUBLE PRECISION); INSERT INTO replacement VALUES(1); INSERT INTO replacement VALUES('1'::double precision/'0'::double precision)", "CREATE TABLE replacement(d DOUBLE PRECISION); INSERT INTO replacement VALUES(1); INSERT INTO replacement VALUES('oops')"} {
			if err := stuntgres.Restore(p.ctx, db, strings.NewReader(script)); err == nil {
				t.Fatal("failed replacement published")
			}
			run(expected)
		}
		canceled, cancel := context.WithCancel(p.ctx)
		cancel()
		if err := stuntgres.Restore(canceled, db, strings.NewReader("CREATE TABLE replacement(t TEXT)")); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		run(expected)
		if err := stuntgres.Restore(p.ctx, db, strings.NewReader("CREATE TABLE replacement(t TEXT); INSERT INTO replacement VALUES('new')")); err != nil {
			t.Fatal(err)
		}
		if len(meta) != 1 || meta[0].DatabaseTypeName() != "FLOAT4" {
			t.Fatal(meta)
		}
		var value float64
		if !held.Next() || held.Scan(&value) != nil || value != 33554432 || held.Next() || held.Err() != nil {
			t.Fatal(value, held.Err())
		}
	})
}
