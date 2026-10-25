package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type hstoreIdentity struct{ scalar, array, input, output, namespace, owner int64 }

func hstoreIdentityCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema string) hstoreIdentity {
	t.Helper()
	var id hstoreIdentity
	var name, kind, category, align, storage string
	var length, collation int64
	var byval bool
	q := "SELECT oid,typarray,typinput::oid,typoutput::oid,typnamespace,typowner,typname,typtype,typcategory,typalign,typstorage,typlen,typcollation,typbyval FROM pg_type WHERE oid=(" + hstoreLiteral(schema+".hstore") + "::regtype)::oid"
	if err := c.QueryRowContext(p.Ctx, q).Scan(&id.scalar, &id.array, &id.input, &id.output, &id.namespace, &id.owner, &name, &kind, &category, &align, &storage, &length, &collation, &byval); err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, oid := range []int64{id.scalar, id.array, id.input, id.output} {
		if oid <= 0 || seen[oid] {
			t.Fatalf("invalid allocated identities %#v", id)
		}
		seen[oid] = true
	}
	if name != "hstore" || kind != "b" || category != "U" || align != "i" || storage != "x" || length != -1 || collation != 0 || byval || id.owner <= 0 {
		t.Fatalf("hstore physical facts %q/%q/%q/%q/%q/%d/%d/%v owner=%d", name, kind, category, align, storage, length, collation, byval, id.owner)
	}
	for _, s := range []fixture.Step{
		fixture.Select(fmt.Sprintf("SELECT nspname FROM pg_namespace WHERE oid=%d", id.namespace), []string{"nspname"}, []string{"NAME"}, [][]any{{schema}}),
		fixture.Select(fmt.Sprintf("SELECT typname,typelem=%d AS reciprocal FROM pg_type WHERE oid=%d", id.scalar, id.array), []string{"typname", "reciprocal"}, []string{"NAME", "BOOL"}, [][]any{{"_hstore", true}}),
		fixture.Select(fmt.Sprintf("SELECT extname,extversion,extrelocatable,extnamespace=%d AS namespace,extowner>0 AS owner FROM pg_extension WHERE extname='hstore'", id.namespace), []string{"extname", "extversion", "extrelocatable", "namespace", "owner"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"hstore", "1.8", true, true, true}}),
		fixture.Select(fmt.Sprintf("SELECT proname,proisstrict,provolatile,proparallel,prorettype=%d AS returns_hstore,pronamespace=%d AS namespace,pronargs FROM pg_proc WHERE oid IN (%d,%d) ORDER BY proname", id.scalar, id.namespace, id.input, id.output), []string{"proname", "proisstrict", "provolatile", "proparallel", "returns_hstore", "namespace", "pronargs"}, []string{"NAME", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL", "INT2"}, [][]any{{"hstore_in", true, "i", "s", true, true, int64(1)}, {"hstore_out", true, "i", "s", false, true, int64(1)}}),
		fixture.Select(fmt.Sprintf("SELECT proargtypes[0]=('cstring'::regtype)::oid AS argument,prorettype=%d AS result FROM pg_proc WHERE oid=%d", id.scalar, id.input), []string{"argument", "result"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}),
		fixture.Select(fmt.Sprintf("SELECT proargtypes[0]=%d AS argument,prorettype=('cstring'::regtype)::oid AS result FROM pg_proc WHERE oid=%d", id.scalar, id.output), []string{"argument", "result"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}),
		fixture.Select(fmt.Sprintf("SELECT deptype FROM pg_depend WHERE classid=1247 AND objid=%d AND refclassid=1247 AND refobjid=%d", id.array, id.scalar), []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}}),
	} {
		hstoreRun(t, p, b, c, s)
	}
	for _, member := range []struct {
		class string
		oid   int64
	}{{"pg_type", id.scalar}, {"pg_proc", id.input}, {"pg_proc", id.output}} {
		q := fmt.Sprintf("SELECT deptype FROM pg_depend WHERE classid='%s'::regclass::oid AND objid=%d AND refclassid='pg_extension'::regclass::oid AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore')", member.class, member.oid)
		hstoreRun(t, p, b, c, fixture.Select(q, []string{"deptype"}, []string{"CHAR"}, [][]any{{"e"}}))
	}
	return id
}

func TestReferenceHstoreInstall(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%v", prepared), func(t *testing.T) {
			hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				s := fixture.Exec("CREATE EXTENSION hstore", 0)
				s.Prepared = prepared
				hstoreRun(t, p, b, c, s)
				s = fixture.Exec("CREATE TABLE hs_install(id integer NOT NULL,v hstore)", 0)
				s.Prepared = prepared
				hstoreRun(t, p, b, c, s)
				hstoreIdentityCheck(t, p, b, c, "public")
			})
		})
	}
	t.Run("qualified", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "hs_namespace")
		hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			hstoreRun(t, p, b, c, fixture.Exec("CREATE SCHEMA hs_namespace", 0))
			hstoreRun(t, p, b, c, fixture.Exec("CREATE EXTENSION hstore SCHEMA hs_namespace", 0))
			hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_qualified(v hs_namespace.hstore)", 0))
			hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_qualified VALUES ('a=>1')", 1))
			hstoreRun(t, p, b, c, hstoreSelect(b, "SELECT v FROM hs_qualified", []string{"v"}, [][]any{{`"a"=>"1"`}}, []bool{true}))
			hstoreIdentityCheck(t, p, b, c, "hs_namespace")
		})
	})
}

func TestReferenceHstoreMetadata(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE hs_meta(id integer NOT NULL,v hstore,n hstore NOT NULL)", 0),
			fixture.Exec("CREATE TABLE hs_empty(v hstore,n hstore NOT NULL)", 0),
			fixture.Exec("CREATE TABLE hs_null(v hstore)", 0),
			fixture.Exec("INSERT INTO hs_meta VALUES (1,'a=>NULL',''),(2,NULL,'a=>\"NULL\"')", 2),
			fixture.Exec("INSERT INTO hs_null VALUES (NULL)", 1),
		} {
			hstoreRun(t, p, b, c, s)
		}
		for _, tc := range []struct {
			q        string
			names    []string
			rows     [][]any
			nullable []bool
		}{
			{"SELECT v,n FROM hs_meta ORDER BY id", []string{"v", "n"}, [][]any{{`"a"=>NULL`, ""}, {nil, `"a"=>"NULL"`}}, []bool{true, false}},
			{"SELECT v,n FROM hs_meta WHERE id=99", []string{"v", "n"}, [][]any{}, []bool{true, false}},
			{"SELECT v,n FROM hs_empty", []string{"v", "n"}, [][]any{}, []bool{true, false}},
			{"SELECT v FROM hs_null", []string{"v"}, [][]any{{nil}}, []bool{true}},
			{"SELECT NULL::hstore AS v", []string{"v"}, [][]any{{nil}}, nil},
		} {
			for _, route := range []string{"direct", "fresh", "held"} {
				s := hstoreSelect(b, tc.q, tc.names, tc.rows, tc.nullable)
				s.Prepared = route == "fresh"
				var cached []*sql.ColumnType
				check := s.CheckMetadata
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					check(t, rows)
					var err error
					cached, err = rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
				}
				if route == "held" {
					stmt, err := c.PrepareContext(p.Ctx, tc.q)
					if err != nil {
						t.Fatal(err)
					}
					hstoreRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}, s)
					stmt.Close()
				} else {
					hstoreRun(t, p, b, c, s)
				}
				hstoreColumns(t, b.PG, cached, tc.names, tc.nullable)
			}
		}
		hstoreIdentityCheck(t, p, b, c, "public")
		hstoreRun(t, p, b, c, fixture.Select("SELECT attname,atttypid=('hstore'::regtype)::oid AS identity,attnotnull FROM pg_attribute WHERE attrelid='hs_meta'::regclass::oid AND attnum>1 AND NOT attisdropped ORDER BY attnum", []string{"attname", "identity", "attnotnull"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"v", true, false}, {"n", true, true}}))
	})
	t.Run("identity_generation", func(t *testing.T) {
		hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			hstoreInstall(t, p, b, c)
			hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_generation(id integer NOT NULL,v hstore DEFAULT 'd=>default')", 0))
			hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_generation VALUES (1,$1)", 1, "a=>original"))
			identity := hstoreIdentityCheck(t, p, b, c, "public")
			q := "SELECT id,v FROM hs_generation ORDER BY id"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			check := hstoreSelect(b, q, []string{"id", "v"}, [][]any{{int64(1), `"a"=>"original"`}}, []bool{false, true})
			var cached []*sql.ColumnType
			check.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				var err error
				cached, err = rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				hstoreColumns(t, b.PG, cached, check.Columns, []bool{false, true})
			}
			hstoreRun(t, p, b, held, check)
			hstoreRun(t, p, b, c, fixture.Exec("ALTER TYPE hstore RENAME TO hs_renamed", 0))
			hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_generation(id) VALUES (2)", 1))
			hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_generation VALUES (3,$1)", 1, `a=>"NULL"`))
			next := hstoreSelect(b, q, []string{"id", "v"}, [][]any{{int64(1), `"a"=>"original"`}, {int64(2), `"d"=>"default"`}, {int64(3), `"a"=>"NULL"`}}, []bool{false, true})
			hstoreRun(t, p, b, c, next)
			hstoreRun(t, p, b, held, next)
			hstoreColumns(t, b.PG, cached, check.Columns, []bool{false, true})
			hstoreRun(t, p, b, c, fixture.Select("SELECT oid=$1::oid AS same_identity,typarray=$2::oid AS same_array FROM pg_type WHERE oid=('hs_renamed'::regtype)::oid", []string{"same_identity", "same_array"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, identity.scalar, identity.array))
		})
	})
}
