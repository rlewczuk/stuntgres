package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// The native bootstrap principal is postgres; the pinned fixture is configured
// with WithUsername("stuntgres") in fixture/lifecycle.go. These are independent
// fixture expectations, never names learned from a query result.
func objectACLOwner(b fixture.Backend) string {
	if b.PG {
		return "stuntgres"
	}
	return "postgres"
}

// Expectations are specified independently from the PG18 release contract.
// The first pinned run must settle the source-derived boundaries and diagnostics.
func objectACLScenario(t *testing.T, roles []string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, roles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, role := range roles {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE ROLE "+role, 0))
			}
			body(t, p, b, c)
		})
	}
}

func objectACLRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	if s.SelectRows && s.State == "" {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	}
}

func objectACLSelect(q string, values ...any) fixture.Step {
	rows := make([][]any, len(values))
	for i, value := range values {
		rows[i] = []any{value}
	}
	return fixture.Select(q, []string{"acl"}, []string{"_ACLITEM"}, rows)
}

func objectACLMetadata(t *testing.T, rows *sql.Rows, pg bool, typ string) {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, []string{"acl"}) {
		t.Fatal("column labels", cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 {
		t.Fatal("column metadata", types, err)
	}
	col := types[0]
	wantScan := reflect.TypeFor[string]()
	if pg {
		wantScan = reflect.TypeFor[any]()
	}
	if col.DatabaseTypeName() != typ || col.ScanType() != wantScan {
		t.Fatalf("descriptor type=%s scan=%v want %s/%v", col.DatabaseTypeName(), col.ScanType(), typ, wantScan)
	}
	if n, known := col.Length(); n != 0 || known {
		t.Fatal("unexpected length", n, known)
	}
	if precision, scale, known := col.DecimalSize(); precision != 0 || scale != 0 || known {
		t.Fatal("unexpected decimal size", precision, scale, known)
	}
	nullable, known := col.Nullable()
	if pg || typ == "ACLITEM" {
		// Expanded unnest scalars retain the existing computed-expression
		// metadata; this inquiry does not widen unnest's nullability promise.
		if nullable || known {
			t.Fatal("computed/pq nullable must be unknown", nullable, known)
		}
	} else if !nullable || !known {
		t.Fatal("native inquiry array must be known nullable", nullable, known)
	}
}
