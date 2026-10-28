package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func enforcementRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
}

func enforcementFlags(table, name string, enforced, valid bool) fixture.Step {
	return fixture.Select("SELECT conenforced,convalidated,condeferrable,condeferred FROM pg_constraint WHERE conrelid=$1::regclass::oid AND conname=$2", []string{"conenforced", "convalidated", "condeferrable", "condeferred"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{enforced, valid, false, false}}, table, name)
}

func enforcementIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table, name, definition string, enforced, valid bool) int64 {
	t.Helper()
	var oid int64
	var def string
	if err := c.QueryRowContext(p.Ctx, "SELECT oid,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass::oid AND conname=$2", table, name).Scan(&oid, &def); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || def != definition {
		t.Fatalf("%s.%s identity/definition = %d %q, want positive OID, %q", table, name, oid, def, definition)
	}
	enforcementRun(t, p, b, c)(enforcementFlags(table, name, enforced, valid))
	return oid
}

func enforcementCheck(table, name string, historical bool, row string) fixture.Diagnostic {
	message := `new row for relation "` + table + `" violates check constraint "` + name + `"`
	if historical {
		message = `check constraint "` + name + `" of relation "` + table + `" is violated by some row`
	}
	d := fixture.ControlsReferenceDiagnostic("23514", message)
	d.Schema, d.Table, d.Constraint = "public", table, name
	if !historical {
		d.Detail = "Failing row contains (" + row + ")."
	}
	return d
}

// A separate snapshot preserves the prerequisite helpers' validity-only contract.
// Include every constraint's flags/origins/provider links, real trigger identities,
// dependency edges, backing relations and data. Native Dump is an additional proof.
type enforcementState struct {
	catalog, triggers, dependencies, relations, rows [][]any
	dump                                             []byte
}

func enforcementQuery(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, args ...any) [][]any {
	t.Helper()
	r, err := c.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cols, err := r.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]any
	for r.Next() {
		v := make([]any, len(cols))
		dst := make([]any, len(cols))
		for i := range v {
			dst[i] = &v[i]
		}
		if err := r.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		for i := range v {
			v[i] = fixture.Normalize(v[i])
		}
		out = append(out, v)
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func enforcementSnapshot(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) enforcementState {
	t.Helper()
	s := enforcementState{
		catalog:      enforcementQuery(t, p, c, "SELECT oid,conname,conrelid,confrelid,conindid,conkey,confkey,conpfeqop,conppeqop,conffeqop,confupdtype,confdeltype,confmatchtype,conenforced,convalidated,condeferrable,condeferred,conislocal,coninhcount,conparentid,pg_get_constraintdef(oid) FROM pg_constraint WHERE connamespace='public'::regnamespace ORDER BY oid"),
		triggers:     enforcementQuery(t, p, c, "SELECT oid,tgrelid,tgconstraint,tgconstrrelid,tgconstrindid,tgenabled,tgisinternal FROM pg_trigger WHERE tgconstraint IN (SELECT oid FROM pg_constraint WHERE connamespace='public'::regnamespace) ORDER BY oid"),
		dependencies: enforcementQuery(t, p, c, "SELECT classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype FROM pg_depend WHERE (classid='pg_constraint'::regclass AND objid IN (SELECT oid FROM pg_constraint WHERE connamespace='public'::regnamespace)) OR (classid='pg_trigger'::regclass AND objid IN (SELECT oid FROM pg_trigger WHERE tgconstraint IN (SELECT oid FROM pg_constraint WHERE connamespace='public'::regnamespace))) ORDER BY classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype"),
		relations:    addConstraintRelations(t, p, c),
		rows:         enforcementQuery(t, p, c, "SELECT * FROM t ORDER BY 1"),
	}
	if !b.PG {
		s.dump = addConstraintDump(t, p)
	}
	return s
}

func enforcementUnchanged(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, before enforcementState) {
	t.Helper()
	after := enforcementSnapshot(t, p, b, c)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed operation published state\nbefore: %#v\nafter: %#v", before, after)
	}
}

func enforcementTriggers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, oid int64, enabled bool) []int64 {
	t.Helper()
	var child, parent, index int64
	if err := c.QueryRowContext(p.Ctx, "SELECT conrelid,confrelid,conindid FROM pg_constraint WHERE oid=$1", oid).Scan(&child, &parent, &index); err != nil {
		t.Fatal(err)
	}
	r, err := c.QueryContext(p.Ctx, "SELECT oid,tgrelid,tgconstrrelid,tgconstrindid,tgenabled,tgisinternal FROM pg_trigger WHERE tgconstraint=$1 ORDER BY oid", oid)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var ids []int64
	childCount, parentCount := 0, 0
	for r.Next() {
		var id, rel, other, idx int64
		var mode string
		var internal bool
		if err := r.Scan(&id, &rel, &other, &idx, &mode, &internal); err != nil {
			t.Fatal(err)
		}
		if id <= 0 || mode != "O" || !internal || idx != index {
			t.Fatal("invalid implementation trigger", id, mode, internal, idx, index)
		}
		if rel == child && other == parent {
			childCount++
		} else if rel == parent && other == child {
			parentCount++
		} else {
			t.Fatal("unlinked trigger", rel, other, child, parent)
		}
		ids = append(ids, id)
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	want := 0
	if enabled {
		want = 4
	}
	if len(ids) != want {
		t.Fatalf("FK %d triggers %v, want %d", oid, ids, want)
	}
	if enabled && child != parent && (childCount != 2 || parentCount != 2) {
		t.Fatal("missing action/check trigger pair", childCount, parentCount)
	}
	return ids
}

func enforcementNewTriggers(t *testing.T, old, next []int64) {
	t.Helper()
	for _, a := range old {
		for _, b := range next {
			if a == b {
				t.Fatal("reenable reused removed trigger", a)
			}
		}
	}
}

func enforcementPosition(q, token string, last bool) string {
	i := strings.Index(q, token)
	if last {
		i = strings.LastIndex(q, token)
	}
	if i < 0 {
		panic("missing diagnostic token")
	}
	return strconv.Itoa(utf8.RuneCountInString(q[:i]) + 1)
}

func enforcementSameDump(t *testing.T, p *fixture.Pair, want []byte) {
	t.Helper()
	if !bytes.Equal(want, addConstraintDump(t, p)) {
		t.Fatal("native canonical dump changed")
	}
}
