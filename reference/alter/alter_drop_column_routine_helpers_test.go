package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// dcrScenario keeps the notice connection and all SQL witnesses in one shared
// scenario on both backends.
func dcrScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *[]fixture.Diagnostic)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = rec
			body(t, p, b, c, &notices)
		})
	}
}

func dcrRun(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
}

func dcrExec(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, queries ...string) {
	t.Helper()
	for _, q := range queries {
		dcrRun(t, p, b, x, fixture.Exec(q, 0))
	}
}

func dcrSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	dcrExec(t, p, b, c, "CREATE TABLE dcr_t(a integer,b integer,c integer)", "CREATE FUNCTION dcr_body() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT a FROM dcr_t; END")
	dcrRun(t, p, b, c, fixture.Exec("INSERT INTO dcr_t VALUES(7,8,9)", 1))
}

func dcrRestrict(table, column, detail string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column "+column+" of table "+table+" because other objects depend on it")
	d.Detail = detail
	d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	return d
}

func dcrBodyRestrict() fixture.Diagnostic {
	return dcrRestrict("dcr_t", "a", "function dcr_body() depends on column a of table dcr_t")
}

func dcrError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want fixture.Diagnostic, held bool, repetitions ...int) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, want)
	s.ErrorStage = "execute"
	if !held {
		dcrRun(t, p, b, c, s)
		return
	}
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("utility prepare must succeed", err)
	}
	defer stmt.Close()
	x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	count := 1
	if len(repetitions) != 0 {
		count = repetitions[0]
	}
	for range count {
		dcrRun(t, p, b, x, s)
	}
}

func dcrNotices(t *testing.T, notices *[]fixture.Diagnostic, objects ...string) {
	t.Helper()
	var want []fixture.Diagnostic
	if len(objects) != 0 {
		d := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000"}
		if len(objects) == 1 {
			d.Message = "drop cascades to " + objects[0]
		} else {
			d.Message = fmt.Sprintf("drop cascades to %d other objects", len(objects))
			var lines []string
			for _, o := range objects {
				lines = append(lines, "drop cascades to "+o)
			}
			d.Detail = strings.Join(lines, "\n")
		}
		want = append(want, d)
	}
	if !reflect.DeepEqual(*notices, want) {
		t.Errorf("complete notices = %#v; want %#v", *notices, want)
	}
	*notices = nil
}

func dcrValue(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, value any) {
	t.Helper()
	s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{value}})
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal(columns, err)
		}
		column := columns[0]
		want := reflect.TypeOf(int64(0))
		if b.PG {
			want = reflect.TypeOf(int32(0))
		}
		if column.ScanType() != want {
			t.Errorf("ScanType = %v; want %v", column.ScanType(), want)
		}
		if _, ok := column.Length(); ok {
			t.Error("INT4 length unexpectedly known")
		}
		if _, _, ok := column.DecimalSize(); ok {
			t.Error("INT4 decimal size unexpectedly known")
		}
		if _, ok := column.Nullable(); ok {
			t.Error("computed nullability unexpectedly known")
		}
	}
	dcrRun(t, p, b, x, s)
}

// dcrRows captures a complete same-backend identity/edge bag, never parity or
// allocator equality between engines. SQL must provide a deterministic ORDER BY.
func dcrRows(t *testing.T, p *fixture.Pair, x fixture.Executor, q string) [][]any {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

const dcrSnapshotSQL = "SELECT d.classid::bigint,d.objid::bigint,d.objsubid,d.refclassid::bigint,d.refobjid::bigint,d.refobjsubid,d.deptype FROM pg_depend d WHERE d.objid IN (SELECT oid FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname LIKE 'dcr_%') OR d.refobjid='dcr_t'::regclass ORDER BY 1,2,3,4,5,6,7"

func dcrUnchanged(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, before [][]any) {
	t.Helper()
	if got := dcrRows(t, p, x, dcrSnapshotSQL); !reflect.DeepEqual(got, before) {
		t.Errorf("dependency bag changed: got %v; want %v", got, before)
	}
	dcrRun(t, p, b, x, fixture.Select("SELECT * FROM dcr_t", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(8), int64(9)}}))
	dcrValue(t, p, b, x, "SELECT dcr_body() AS v", int64(7))
}

func dcrAttributeEdges(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, signature, relation string, attrs ...int64) {
	t.Helper()
	proc := dcrRows(t, p, x, "SELECT '"+signature+"'::regprocedure::oid::bigint")[0][0]
	rel := dcrRows(t, p, x, "SELECT '"+relation+"'::regclass::oid::bigint")[0][0]
	var want [][]any
	for _, attr := range attrs {
		want = append(want, []any{int64(1255), proc, int64(0), int64(1259), rel, attr, "n"})
	}
	q := "SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='" + signature + "'::regprocedure AND refclassid='pg_class'::regclass AND refobjid='" + relation + "'::regclass AND refobjsubid>0 ORDER BY refobjsubid"
	dcrRun(t, p, b, x, fixture.Select(q, []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, want))
}

func dcrInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, names ...string) {
	t.Helper()
	var want [][]any
	for _, name := range names {
		want = append(want, []any{name})
	}
	dcrRun(t, p, b, x, fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname LIKE 'dcr_%' ORDER BY proname", []string{"proname"}, []string{"NAME"}, want))
}
