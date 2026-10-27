package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func loIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	run := func(s fixture.Step) { loRun(t, p, b, db, s) }
	run(fixture.Select("SELECT typtype,typbasetype::bigint AS base,typcategory,typispreferred,typlen,typbyval,typnotnull,typtypmod,typndims,typdefault IS NULL AS no_default,typarray<>0 AS companion FROM pg_type WHERE oid='lo'::regtype", []string{"typtype", "base", "typcategory", "typispreferred", "typlen", "typbyval", "typnotnull", "typtypmod", "typndims", "no_default", "companion"}, []string{"CHAR", "INT8", "CHAR", "BOOL", "INT2", "BOOL", "BOOL", "INT4", "INT4", "BOOL", "BOOL"}, [][]any{{"d", int64(26), "N", false, int64(4), true, false, int64(-1), int64(0), true, true}}))
	run(fixture.Select("SELECT d.typarray=a.oid AS array_link,a.typelem=d.oid AS element_link,a.typnamespace=d.typnamespace AS same_schema,a.typowner=d.typowner AS same_owner FROM pg_type d JOIN pg_type a ON a.oid=d.typarray WHERE d.oid='lo'::regtype AND a.oid='lo[]'::regtype", []string{"array_link", "element_link", "same_schema", "same_owner"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
	run(fixture.Select("SELECT conname FROM pg_constraint WHERE contypid='lo'::regtype ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{}))
	run(fixture.Select("SELECT t.typname FROM pg_depend d JOIN pg_type t ON t.oid=d.objid WHERE d.classid=1247 AND d.refclassid=3079 AND d.deptype='e' AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='lo') AND t.oid IN ('lo'::regtype,'lo[]'::regtype) ORDER BY t.typname", []string{"typname"}, []string{"NAME"}, [][]any{{"_lo"}, {"lo"}}))
	run(fixture.Select("SELECT classid::bigint,objsubid,refclassid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid=1247 AND objid='lo[]'::regtype AND refclassid=1247 AND refobjid='lo'::regtype AND deptype='i'", []string{"classid", "objsubid", "refclassid", "refobjsubid", "deptype"}, []string{"INT8", "INT4", "INT8", "INT4", "CHAR"}, [][]any{{int64(1247), int64(0), int64(1247), int64(0), "i"}}))
	// Builtin oid is pinned: PostgreSQL need not store a normal dependency edge.
	run(fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1247 AND objid='lo'::regtype AND refclassid=1247 AND refobjid=26", []string{"deptype"}, []string{"CHAR"}, [][]any{}))
}

func TestReferenceLoIdentityMetadata(t *testing.T) {
	loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		loInstall(t, p, b, c)
		loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_metadata(id integer NOT NULL,v lo,n lo NOT NULL)", 0))
		loRun(t, p, b, c, fixture.Exec("INSERT INTO lo_metadata VALUES (1,'4294967295','26'),(2,NULL,'0')", 2))
		loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_empty(v lo,n lo NOT NULL)", 0))
		for _, tc := range []struct {
			q        string
			names    []string
			rows     [][]any
			nullable []bool
			args     []any
		}{
			{"SELECT v,n FROM lo_metadata ORDER BY id", []string{"v", "n"}, [][]any{{int64(4294967295), int64(26)}, {nil, int64(0)}}, []bool{true, false}, nil},
			{"SELECT v FROM lo_metadata WHERE id=2", []string{"v"}, [][]any{{nil}}, []bool{true}, nil},
			{"SELECT v,n FROM lo_empty", []string{"v", "n"}, [][]any{}, []bool{true, false}, nil},
			{"SELECT v::lo AS casted FROM lo_metadata WHERE id=1", []string{"casted"}, [][]any{{int64(4294967295)}}, []bool{true}, nil},
			{"SELECT NULL::lo AS absent FROM lo_metadata WHERE id=1", []string{"absent"}, [][]any{{nil}}, nil, nil},
			{"SELECT $1::lo AS bound FROM lo_metadata WHERE id=1", []string{"bound"}, [][]any{{int64(2147483648)}}, nil, []any{"2147483648"}},
			{"SELECT $1::lo AS bound FROM lo_metadata WHERE id=1", []string{"bound"}, [][]any{{nil}}, nil, []any{nil}},
		} {
			types := make([]string, len(tc.names))
			for i := range types {
				types[i] = "OID"
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(tc.q, tc.names, types, tc.rows, tc.args...)
				s.Prepared = prepared
				loMetadataRows(t, p, b, c, s, tc.nullable)
			}
		}
		loIdentity(t, p, b, c)
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT t.typname AS logical,t.typtype AS kind,t.typbasetype::bigint AS base FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid WHERE a.attrelid='lo_metadata'::regclass AND a.attname='v'", []string{"logical", "kind", "base"}, []string{"NAME", "CHAR", "INT8"}, [][]any{{"lo", "d", int64(26)}})
			s.Prepared = prepared
			loRun(t, p, b, c, s)
			s = fixture.Select("SELECT pg_typeof(v)::text AS logical,pg_typeof(v::oid)::text AS physical,pg_typeof(NULL::lo)::text AS typed_null FROM lo_metadata WHERE id=2", []string{"logical", "physical", "typed_null"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"lo", "oid", "lo"}})
			s.Prepared = prepared
			loRun(t, p, b, c, s)
		}
		loRun(t, p, b, c, fixture.Select("SELECT atttypid='lo'::regtype AS logical,atttypid<>26 AS not_base FROM pg_attribute WHERE attrelid='lo_metadata'::regclass AND attname='v'", []string{"logical", "not_base"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	})
}
