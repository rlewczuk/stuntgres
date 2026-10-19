package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Version scenarios use the shared fixture, including its driver, recorder and
// bounded context. Only oracle setup uses unsupported DROP/routine DDL.
func extensionVersionCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"ev_owner", "ev_other"})
	fixture.SchemaReferenceCleanup(t, "ev_addon")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS fuzzystrmatch CASCADE", "DROP SCHEMA IF EXISTS ev_addon CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
					for _, role := range []string{"ev_owner", "ev_other"} {
						var n int
						if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname=$1", role).Scan(&n); err != nil {
							t.Error(err)
							continue
						}
						if n != 0 {
							for _, q := range []string{"DROP OWNED BY " + role, "DROP ROLE " + role} {
								if _, err := c.ExecContext(p.Ctx, q); err != nil {
									t.Error(q, err)
								}
							}
						}
					}
				}
				clean()
				defer clean()
			}
			body(t, p, b, c)
		})
	}
}

func extensionVersionBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func extensionVersionHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}

func extensionVersionAvailability(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, installed any) {
	t.Helper()
	extensionVersionBoth(t, p, b, c, fixture.Select("SELECT name,default_version,installed_version,comment FROM pg_available_extensions WHERE name='fuzzystrmatch'", []string{"name", "default_version", "installed_version", "comment"}, []string{"NAME", "TEXT", "TEXT", "TEXT"}, [][]any{{"fuzzystrmatch", "1.2", installed, "determine similarities and distance between strings"}}))
	rows := [][]any{}
	for _, v := range []string{"1.1", "1.2"} {
		rows = append(rows, []any{"fuzzystrmatch", v, installed == v, true, true, true, nil, nil, "determine similarities and distance between strings"})
	}
	extensionVersionBoth(t, p, b, c, fixture.Select("SELECT name,version,installed,superuser,trusted,relocatable,schema,requires,comment FROM pg_available_extension_versions WHERE name='fuzzystrmatch' ORDER BY version", []string{"name", "version", "installed", "superuser", "trusted", "relocatable", "schema", "requires", "comment"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "NAME", "_NAME", "TEXT"}, rows))
}

type extensionVersionIdentity struct {
	Extension, Namespace, Owner int64
	Members                     map[int64][]any
}

// Ignore the upstream-only text_soundex alias, never sibling overloads.
func extensionVersionInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, version string) extensionVersionIdentity {
	t.Helper()
	x := extensionVersionIdentity{Members: map[int64][]any{}}
	var got string
	var reloc bool
	var config, condition any
	err := c.QueryRowContext(p.Ctx, "SELECT oid,extnamespace,extowner,extversion,extrelocatable,extconfig,extcondition FROM pg_extension WHERE extname='fuzzystrmatch'").Scan(&x.Extension, &x.Namespace, &x.Owner, &got, &reloc, &config, &condition)
	if err != nil || x.Extension <= 0 || x.Namespace <= 0 || x.Owner <= 0 || got != version || !reloc || config != nil || condition != nil {
		t.Fatal("extension identity", x, got, reloc, config, condition, err)
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid,p.proname,p.pronamespace,p.proowner,p.pronargs,p.prorettype,p.provolatile,p.proisstrict,p.proparallel FROM pg_proc p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid=1255 AND d.refclassid=3079 AND d.refobjid=$1 AND d.deptype='e' AND p.proname<>'text_soundex' ORDER BY p.oid", x.Extension)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var oid, ns, owner, nargs, result int64
		var name, vol, parallel string
		var strict bool
		if err := rows.Scan(&oid, &name, &ns, &owner, &nargs, &result, &vol, &strict, &parallel); err != nil {
			t.Fatal(err)
		}
		wantResult := int64(25)
		switch name {
		case "levenshtein", "levenshtein_less_equal", "difference":
			wantResult = 23
		case "daitch_mokotoff":
			wantResult = 1009
		case "soundex", "metaphone", "dmetaphone", "dmetaphone_alt":
		default:
			t.Fatal("unexpected selected member", name)
		}
		if oid <= 0 || ns != x.Namespace || owner <= 0 || result != wantResult || vol != "i" || !strict || parallel != "s" {
			t.Fatal("member properties", oid, name, ns, owner, nargs, result, vol, strict, parallel)
		}
		wantArity := map[string][]int64{"levenshtein": {2, 5}, "levenshtein_less_equal": {3, 6}, "difference": {2}, "metaphone": {2}, "soundex": {1}, "dmetaphone": {1}, "dmetaphone_alt": {1}, "daitch_mokotoff": {1}}
		valid := false
		for _, n := range wantArity[name] {
			valid = valid || nargs == n
		}
		if !valid {
			t.Fatal("member arity", name, nargs)
		}
		if _, duplicate := x.Members[oid]; duplicate {
			t.Fatal("duplicate member edge", oid)
		}
		x.Members[oid] = []any{name, ns, owner, nargs, result, vol, strict, parallel}
		counts[name]++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantCounts := map[string]int{"levenshtein": 2, "levenshtein_less_equal": 2, "difference": 1, "metaphone": 1, "soundex": 1, "dmetaphone": 1, "dmetaphone_alt": 1}
	if version == "1.2" {
		wantCounts["daitch_mokotoff"] = 1
	}
	if !reflect.DeepEqual(counts, wantCounts) {
		t.Fatal("selected version manifest", counts, wantCounts)
	}
	return x
}

func extensionVersionPreserved(t *testing.T, before, after extensionVersionIdentity) {
	t.Helper()
	if before.Extension != after.Extension || before.Namespace != after.Namespace || before.Owner != after.Owner {
		t.Fatal("extension identity changed", before, after)
	}
	for id, old := range before.Members {
		if !reflect.DeepEqual(old, after.Members[id]) {
			t.Fatal("old member changed", id, old, after.Members[id])
		}
	}
}

func extensionVersionBase() fixture.Step {
	return fixture.Select("SELECT levenshtein('kitten','sitting'),metaphone('GUMBO',4),soundex('John')", []string{"levenshtein", "metaphone", "soundex"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(3), "KM", "J500"}})
}

func extensionVersionMissing(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	s := extensionMissing("SELECT daitch_mokotoff('John')", "daitch_mokotoff", "unknown")
	s.SelectRows = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	s = fixture.Prepared(s)
	s.ErrorStage = "prepare"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func extensionVersionDM(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT daitch_mokotoff($1::text)"
	held := extensionVersionHeld(t, p, c, q)
	for _, tc := range []struct{ in, out any }{{"John", "{160000,460000}"}, {"George", "{595000}"}, {nil, nil}, {"", nil}, {"John", "{160000,460000}"}} {
		s := daitchSelect(q, tc.out, b.PG, tc.in)
		arrayMetadata := s.CheckMetadata
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			arrayMetadata(t, rows)
			columns, err := rows.ColumnTypes()
			if err != nil || len(columns) != 1 {
				t.Fatal("DM metadata width", err)
			}
			nullable, known := columns[0].Nullable()
			precision, scale, decimal := columns[0].DecimalSize()
			// Computed function output has no declared NOT NULL provenance.
			// pq likewise reports unknown, not an invented nullable guarantee.
			if nullable || known || precision != 0 || scale != 0 || decimal {
				t.Fatal("DM nullable/decimal metadata", nullable, known, precision, scale, decimal)
			}
		}
		extensionVersionBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}

func extensionVersionPathRows() [][]any {
	return [][]any{{"1.0", "1.1", "1.0--1.1"}, {"1.0", "1.2", "1.0--1.1--1.2"}, {"1.1", "1.0", nil}, {"1.1", "1.2", "1.1--1.2"}, {"1.2", "1.0", nil}, {"1.2", "1.1", nil}}
}
func extensionVersionPaths(q string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"source", "target", "path"}, []string{"TEXT", "TEXT", "TEXT"}, rows, args...)
}
