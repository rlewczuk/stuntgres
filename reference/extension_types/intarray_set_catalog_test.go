package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func intarraySetCatalog(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, owner string) []int64 {
	t.Helper()
	// Retain every preexisting type and I/O assertion; the selected family appends
	// declarations, it does not replace the sealed provider prefix.
	intarrayDeclarationChecks(t, p, b, c, owner)
	var ids []int64
	proc := func(name, args string) string { return "('ia_provider." + name + "(" + args + ")'::regprocedure)::oid" }
	op := func(name, right string) string {
		return "('ia_provider." + name + "(integer[]," + right + ")'::regoperator)::oid"
	}
	ext := "(SELECT oid FROM pg_extension WHERE extname='intarray')"
	member := func(class, identity string) {
		q := "SELECT refobjid=" + ext + " AS extension FROM pg_depend WHERE classid=" + class + " AND objid=" + identity + " AND objsubid=0 AND refclassid=3079 AND refobjsubid=0 AND deptype='e'"
		intarrayBoth(t, p, b, c, fixture.Select(q, []string{"extension"}, []string{"BOOL"}, [][]any{{true}}))
	}
	readID := func(identity string) {
		var id int64
		if err := c.QueryRowContext(p.Ctx, "SELECT "+identity).Scan(&id); err != nil || id <= 0 {
			t.Fatalf("identity %s=%d: %v", identity, id, err)
		}
		for _, previous := range ids {
			if previous == id {
				t.Fatalf("selected identities alias %d", id)
			}
		}
		ids = append(ids, id)
	}
	for _, tc := range intarraySetCases {
		args, result := "1007 1007", "1007"
		if tc.rightType == "integer" {
			args = "1007 23"
		}
		if tc.family == "BOOL" {
			result = "16"
		}
		identity := proc(tc.name, "integer[],"+tc.rightType)
		readID(identity)
		q := "SELECT proname,proargtypes::text AS args,prorettype::text AS result,pronargs,proretset,proisstrict,provolatile,proparallel,l.lanname,prosrc,probin,r.rolname FROM (SELECT * FROM pg_proc WHERE oid=" + identity + ") p JOIN pg_language l ON l.oid=p.prolang JOIN pg_roles r ON r.oid=p.proowner"
		intarrayBoth(t, p, b, c, fixture.Select(q, []string{"proname", "args", "result", "pronargs", "proretset", "proisstrict", "provolatile", "proparallel", "lanname", "prosrc", "probin", "rolname"}, []string{"NAME", "TEXT", "TEXT", "INT2", "BOOL", "BOOL", "CHAR", "CHAR", "NAME", "TEXT", "TEXT", "NAME"}, [][]any{{tc.name, args, result, int64(2), false, true, "i", "s", "c", tc.name, "$libdir/_int", owner}}))
		member("1255", identity)
	}
	for _, base := range []string{"_int_overlap", "_int_contains", "_int_contained"} {
		for _, join := range []bool{false, true} {
			name, args, oids, nargs := base+"_sel", "internal,oid,internal,integer", "2281 26 2281 23", int64(4)
			if join {
				name, args, oids, nargs = base+"_joinsel", "internal,oid,internal,smallint,internal", "2281 26 2281 21 2281", int64(5)
			}
			identity := proc(name, args)
			readID(identity)
			q := "SELECT proname,proargtypes::text AS args,prorettype::text AS result,pronargs,proretset,proisstrict,provolatile,proparallel,l.lanname,prosrc,probin,r.rolname FROM (SELECT * FROM pg_proc WHERE oid=" + identity + ") p JOIN pg_language l ON l.oid=p.prolang JOIN pg_roles r ON r.oid=p.proowner"
			intarrayBoth(t, p, b, c, fixture.Select(q, []string{"proname", "args", "result", "pronargs", "proretset", "proisstrict", "provolatile", "proparallel", "lanname", "prosrc", "probin", "rolname"}, []string{"NAME", "TEXT", "TEXT", "INT2", "BOOL", "BOOL", "CHAR", "CHAR", "NAME", "TEXT", "TEXT", "NAME"}, [][]any{{name, oids, "701", nargs, false, true, "s", "s", "c", name, "$libdir/_int", owner}}))
			member("1255", identity)
		}
	}
	for _, tc := range intarraySetCases {
		identity := op(tc.op, tc.rightType)
		readID(identity)
		commutator := "0::oid"
		switch tc.name {
		case "_int_overlap", "intarray_push_array", "_int_union", "_int_inter":
			commutator = identity
		case "_int_contains":
			commutator = op("<@", "integer[]")
		case "_int_contained":
			commutator = op("@>", "integer[]")
		}
		restrictor, joiner := "0::oid", "0::oid"
		if tc.family == "BOOL" {
			restrictor = proc(tc.name+"_sel", "internal,oid,internal,integer")
			joiner = proc(tc.name+"_joinsel", "internal,oid,internal,smallint,internal")
		}
		right, result := "1007", "1007"
		if tc.rightType == "integer" {
			right = "23"
		}
		if tc.family == "BOOL" {
			result = "16"
		}
		q := "SELECT oprname,oprcode::oid=" + proc(tc.name, "integer[],"+tc.rightType) + " AS backer,oprcom=" + commutator + " AS commutator,oprnegate=0::oid AS negator,oprleft=1007::oid AS leftarg,oprright=" + right + "::oid AS rightarg,oprresult=" + result + "::oid AS result,oprnamespace='ia_provider'::regnamespace AS namespace,r.rolname,oprcanhash,oprcanmerge,oprrest::oid=" + restrictor + " AS restrictor,oprjoin::oid=" + joiner + " AS joiner FROM (SELECT * FROM pg_operator WHERE oid=" + identity + ") o JOIN pg_roles r ON r.oid=o.oprowner"
		intarrayBoth(t, p, b, c, fixture.Select(q, []string{"oprname", "backer", "commutator", "negator", "leftarg", "rightarg", "result", "namespace", "rolname", "oprcanhash", "oprcanmerge", "restrictor", "joiner"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{tc.op, true, true, true, true, true, true, true, owner, false, false, true, true}}))
		member("2617", identity)
		for _, dependency := range []string{proc(tc.name, "integer[],"+tc.rightType), restrictor, joiner} {
			if dependency == "0::oid" {
				continue
			}
			q := "SELECT refobjid=" + dependency + " AS routine FROM pg_depend WHERE classid=2617 AND objid=" + identity + " AND objsubid=0 AND refclassid=1255 AND refobjid=" + dependency + " AND refobjsubid=0 AND deptype='n'"
			intarrayBoth(t, p, b, c, fixture.Select(q, []string{"routine"}, []string{"BOOL"}, [][]any{{true}}))
		}
	}
	intarrayBoth(t, p, b, c, fixture.Select("SELECT 'integer'::regtype::oid=23::oid AS scalar,'integer[]'::regtype::oid=1007::oid AS array", []string{"scalar", "array"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	return ids
}

func TestReferenceIntarraySetCatalog(t *testing.T) {
	intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		intarrayInstall(t, p, b, c)
		ids := intarraySetCatalog(t, p, b, c, intarrayFixtureOwner(b))
		if len(ids) != 26 {
			t.Fatalf("selected declaration identities %d want 26", len(ids))
		}
	})
}
