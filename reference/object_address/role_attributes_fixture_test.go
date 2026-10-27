package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Privileged catalog facts are oracle-only, not a native pg_authid API.
func roleAttributesFacts(t *testing.T, p *fixture.Pair, name string, want []any) {
	t.Helper()
	rows, err := fixture.Admin.QueryContext(p.Ctx, `SELECT rolsuper,rolcreatedb,rolcreaterole,rolcanlogin,rolinherit,rolreplication,rolbypassrls,rolconnlimit,rolpassword IS NULL,rolvaliduntil::text FROM pg_authid WHERE rolname=$1`, name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing oracle role", name, rows.Err())
	}
	got := make([]any, 10)
	dest := make([]any, 10)
	for i := range got {
		dest[i] = &got[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	for i, value := range got {
		if bytes, ok := value.([]byte); ok {
			value = string(bytes)
		}
		if value != want[i] {
			t.Errorf("oracle %s field %d = %v (%T), want %v (%T)", name, i, value, value, want[i], want[i])
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra oracle facts", rows.Err())
	}
}

func roleAttributesDefaults(login bool) []any {
	return []any{false, false, false, login, true, false, false, int64(-1), true, nil}
}

func roleAttributesDenied(query, detail string, prepared bool) fixture.Step {
	return fixture.RoleReferenceError(query, "42501", "permission denied to alter role", detail, prepared, "execute")
}
