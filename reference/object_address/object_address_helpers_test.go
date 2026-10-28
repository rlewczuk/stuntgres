package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Shared contracts are grounded in the pinned PostgreSQL 18.6 oracle.
func objectAddressScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func objectAddressRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	if s.SelectRows && s.State == "" {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	}
}

func objectAddressLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func objectAddressArray(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = objectAddressLiteral(x)
	}
	return "ARRAY[" + strings.Join(parts, ",") + "]::text[]"
}
func objectAddressArrayText(t *testing.T, xs []string) string {
	t.Helper()
	parts := make([]string, len(xs))
	for i, x := range xs {
		if x == "" || strings.EqualFold(x, "NULL") || strings.ContainsAny(x, " ,{}\"\\\t\n\r") {
			x = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(x) + `"`
		}
		parts[i] = x
	}
	return "{" + strings.Join(parts, ",") + "}"
}

type objectAddressValue struct{ class, object, sub int64 }

func objectAddressResolve(t *testing.T, p *fixture.Pair, c fixture.Executor, kind string, names, args []string) objectAddressValue {
	t.Helper()
	q := "SELECT classid,objid,objsubid FROM pg_get_object_address(" + objectAddressLiteral(kind) + "," + objectAddressArray(names) + "," + objectAddressArray(args) + ")"
	var a objectAddressValue
	rows, err := c.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal(q, rows.Err())
	}
	if err := rows.Scan(&a.class, &a.object, &a.sub); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil || a.object == 0 {
		t.Fatal("invalid address", a, rows.Err())
	}
	return a
}

func objectAddressNumeric(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, a objectAddressValue, kind string, schema, name, identity any, names, args any, description any) {
	t.Helper()
	input := fmt.Sprintf("%d::oid,%d::oid,'%d'::int4", a.class, a.object, a.sub)
	for _, s := range []fixture.Step{
		fixture.Select("SELECT * FROM pg_identify_object("+input+")", []string{"type", "schema", "name", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{kind, schema, name, identity}}),
		fixture.Select("SELECT * FROM pg_identify_object_as_address("+input+")", []string{"type", "object_names", "object_args"}, []string{"TEXT", "_TEXT", "_TEXT"}, [][]any{{kind, names, args}}),
		fixture.Select("SELECT pg_describe_object("+input+") AS description", []string{"description"}, []string{"TEXT"}, [][]any{{description}}),
	} {
		t.Run(s.Query, func(t *testing.T) { objectAddressRun(t, p, b, c, s) })
	}
}

// idSQL is independent existing catalog/alias authority, never another selected
// function. Dynamic IDs are local relationships, not cross-backend goldens.
func objectAddressWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, class int64, kind string, names, args []string, schema, name, identity, description any, idSQL string) objectAddressValue {
	t.Helper()
	a := objectAddressResolve(t, p, c, kind, names, args)
	if a.class != class {
		t.Fatalf("%s class %d want %d", kind, a.class, class)
	}
	if idSQL != "" {
		var id int64
		rows, err := c.QueryContext(p.Ctx, idSQL)
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			rows.Close()
			t.Fatal("missing independent authority", idSQL)
		}
		err = rows.Scan(&id)
		more := rows.Next()
		rows.Close()
		if err != nil || more || id != a.object {
			t.Fatal("independent object ownership", id, a, err)
		}
	}
	objectAddressNumeric(t, p, b, c, a, kind, schema, name, identity, objectAddressArrayText(t, names), objectAddressArrayText(t, args), description)
	q := "SELECT classid::bigint AS class,objid::bigint AS object,objsubid FROM pg_get_object_address($1,$2::text[],$3::text[])"
	objectAddressRun(t, p, b, c, fixture.Select(q, []string{"class", "object", "objsubid"}, []string{"INT8", "INT8", "INT4"}, [][]any{{a.class, a.object, a.sub}}, kind, objectAddressArrayText(t, names), objectAddressArrayText(t, args)))
	// The actual returned arrays, not only the independent literals, must invert.
	q = "SELECT g.classid::bigint AS class,g.objid::bigint AS object,g.objsubid FROM pg_identify_object_as_address($1::oid,$2::oid,$3::int4) a, LATERAL pg_get_object_address(a.type,a.object_names,a.object_args) g"
	objectAddressRun(t, p, b, c, fixture.Select(q, []string{"class", "object", "objsubid"}, []string{"INT8", "INT8", "INT4"}, [][]any{{a.class, a.object, a.sub}}, a.class, a.object, a.sub))
	return a
}
