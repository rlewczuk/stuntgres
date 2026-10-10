package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestReferenceQueryLimitMigratedSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			both := func(s referenceStep) { run(s); run(referencePrepared(s)) }
			selectIDs := func(q, column, typ string, ids ...int64) {
				run(referenceSelect(q, []string{column}, []string{typ}, referenceLimitIDs(ids...)))
			}
			run(referenceExec("CREATE TABLE t(id INT NOT NULL,v INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,2),(2,0),(3,3),(4,4)", 4))
			for _, q := range []string{"SELECT id FROM t ORDER BY id LIMIT 2 OFFSET 1", "SELECT id FROM t ORDER BY id OFFSET 1 ROW FETCH NEXT 2 ROWS ONLY"} {
				both(referenceSelect(q, []string{"id"}, []string{"INT4"}, referenceLimitIDs(2, 3)))
			}
			stmt, err := b.db.Prepare("SELECT id FROM t ORDER BY id LIMIT $1 OFFSET $2")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, tc := range []struct {
				count, offset any
				ids           []int64
			}{{2, 1, []int64{2, 3}}, {0, 0, nil}, {nil, 3, []int64{4}}} {
				rows, err := stmt.Query(tc.count, tc.offset)
				referenceViewValues(t, relationOutcome{rows: rows, err: err}, "id", "INT4", tc.ids...)
			}
			badRows, badErr := stmt.Query(1.5, 1)
			if badRows != nil {
				badRows.Close()
				t.Fatal("fractional BIGINT input exposed cursor")
			}
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type bigint: "1.5"`, Context: "unnamed portal parameter $1 = '...'"}
			referenceAssertDiagnostic(t, badErr, &want)
			run(referenceExec("CREATE TABLE typed(f DOUBLE PRECISION)", 0))
			run(referenceExec("INSERT INTO typed VALUES(2.5)", 1))
			both(referenceSelect("SELECT t.id FROM t,typed WHERE typed.f=$1 ORDER BY t.id LIMIT $1 OFFSET 1", []string{"id"}, []string{"INT4"}, referenceLimitIDs(2, 3), float64(2.5)))
			for _, suffix := range []string{"LIMIT ALL", "LIMIT NULL", "OFFSET NULL"} {
				selectIDs("SELECT id FROM t ORDER BY id "+suffix, "id", "INT4", 1, 2, 3, 4)
			}
			selectIDs("SELECT id FROM t ORDER BY id FETCH FIRST ROW ONLY", "id", "INT4", 1)
			selectIDs("SELECT 10/v AS n FROM t LIMIT 1", "n", "INT4", 5)
			selectIDs("SELECT 10/v AS n FROM t ORDER BY id LIMIT 0", "n", "INT4")
			s := referenceImplicitError("SELECT 10/v AS n FROM t ORDER BY id LIMIT 1", referenceDiagnostic{Code: "22012", Message: "division by zero"})
			s.selectRows = true
			run(s)
			run(referenceExec("CREATE SEQUENCE s START WITH 1", 0))
			run(referenceExec("CREATE VIEW drawn WITH (security_barrier=true) AS SELECT nextval('s') AS n FROM t", 0))
			selectIDs("SELECT n FROM drawn LIMIT 0", "n", "INT8")
			selectIDs("SELECT n FROM drawn LIMIT 1", "n", "INT8", 1)
			selectIDs("SELECT nextval('s') AS n", "n", "INT8", 2)
			run(referenceExec("CREATE SEQUENCE bounds_s START WITH 1", 0))
			bounds, err := b.db.Prepare("SELECT id FROM t ORDER BY id LIMIT nextval('bounds_s') OFFSET nextval('bounds_s')")
			if err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT is_called FROM bounds_s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
			rows, err := bounds.Query()
			referenceViewValues(t, relationOutcome{rows: rows, err: err}, "id", "INT4", 2, 3)
			bounds.Close()
			selectIDs("SELECT nextval('bounds_s') AS n", "n", "INT8", 3)
			run(referenceExec("CREATE SEQUENCE demand_s START WITH 1", 0))
			selectIDs("SELECT nextval('demand_s') AS n FROM t ORDER BY id OFFSET 1 LIMIT 1", "n", "INT8", 2)
			selectIDs("SELECT nextval('demand_s') AS n", "n", "INT8", 3)
			for _, tc := range []struct {
				suffix string
				ids    []int64
				next   int64
			}{{"LIMIT 0", nil, 1}, {"LIMIT 1", []int64{1}, 2}, {"OFFSET 1", nil, 2}} {
				run(referenceExec("ALTER SEQUENCE demand_s RESTART WITH 1", 0))
				selectIDs("SELECT nextval('demand_s') "+tc.suffix, "nextval", "INT8", tc.ids...)
				selectIDs("SELECT nextval('demand_s') AS n", "n", "INT8", tc.next)
			}
			run(referenceExec("CREATE SEQUENCE cache_s START WITH 1", 0))
			selectIDs("SELECT nextval('cache_s') AS n FROM t ORDER BY n DESC OFFSET 1 LIMIT 2", "n", "INT8", 3, 2)
			selectIDs("SELECT nextval('cache_s') AS n", "n", "INT8", 5)
			for _, tc := range []struct{ q, code, message, position string }{
				{"SELECT id FROM t LIMIT TRUE", "42804", "argument of LIMIT must be type bigint, not type boolean", "24"},
				{"SELECT id FROM t LIMIT id", "42P10", "argument of LIMIT must not contain variables", "24"},
				{"SELECT id FROM t WHERE FALSE LIMIT -1", "2201W", "LIMIT must not be negative", ""},
			} {
				s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				s.selectRows = true
				run(s)
			}
			referenceTiesCanceledBeforeExecution(t, p.ctx, b.db, "SELECT nextval('cache_s') AS n FROM t LIMIT 2")
			cancelCtx, stop := context.WithCancel(p.ctx)
			cancelled, err := b.db.QueryContext(cancelCtx, "SELECT nextval('cache_s') AS n FROM t LIMIT 2")
			if err != nil {
				stop()
				t.Fatal(err)
			}
			stop()
			referenceTiesCanceledCursor(t, cancelled, b.pg)
			selectIDs("SELECT nextval('cache_s') AS n", "n", "INT8", 8)
			run(referenceExec("CREATE VIEW sliced AS SELECT id FROM t ORDER BY id OFFSET 1 FETCH FIRST 2 ROWS ONLY", 0))
			run(referenceExec("ALTER TABLE t RENAME COLUMN id TO renamed", 0))
			held, err := b.db.QueryContext(p.ctx, "SELECT id FROM sliced LIMIT 1")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if !b.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				run(referenceExec("INSERT INTO t VALUES(5,5)", 1))
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			referenceViewValues(t, relationOutcome{rows: held}, "id", "INT4", 2)
			selectIDs("SELECT id FROM sliced", "id", "INT4", 2, 3)
			run(referenceExec("CREATE TABLE dst(id INT)", 0))
			run(referenceExec("INSERT INTO dst SELECT renamed FROM t ORDER BY renamed OFFSET 1 LIMIT 2", 2))
			selectIDs("SELECT id FROM dst ORDER BY id", "id", "INT4", 2, 3)
			run(referenceExec("CREATE TABLE small(id SMALLINT)", 0))
			run(referenceExec("INSERT INTO small VALUES(1)", 1))
			run(referenceExec("CREATE TABLE failing_default(id INT,n INT DEFAULT 1/0)", 0))
			for _, prepared := range []bool{false, true} {
				for _, suffix := range []string{"LIMIT 0", "LIMIT 1"} {
					s := referenceImplicitError("INSERT INTO failing_default(id) SELECT id FROM small "+suffix, referenceDiagnostic{Code: "22012", Message: "division by zero"})
					s.prepared = prepared
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
					selectIDs("SELECT id FROM failing_default", "id", "INT4")
				}
			}
			own, err := b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer own.Rollback()
			referenceLockExec(t, p.ctx, own, "INSERT INTO small VALUES(4)", 1)
			referenceLockExec(t, p.ctx, own, "INSERT INTO small SELECT id FROM small ORDER BY id DESC LIMIT 1", 1)
			if err := own.Commit(); err != nil {
				t.Fatal(err)
			}
			selectIDs("SELECT id FROM small ORDER BY id", "id", "INT2", 1, 4, 4)
		})
	}
}
