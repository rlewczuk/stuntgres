package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainIdentityLifecycle(t *testing.T) {
	domainIdentityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		domainIdentityDDL(t, p, c, r,
			"CREATE SCHEMA di_src", "CREATE SCHEMA di_dst", "CREATE SCHEMA di_other",
			"CREATE DOMAIN di_src.d AS numeric(5,2) DEFAULT 1.25 CONSTRAINT positive CHECK(VALUE>0)",
			"CREATE DOMAIN di_other.child AS di_src.d",
			"CREATE TYPE di_other.carrier AS (v di_src.d,keep integer)",
			"CREATE TABLE di_other.t(id integer,v di_src.d,keep integer,c di_other.child,a di_src.d[],box di_other.carrier)",
			"CREATE TABLE di_other.recipes(v integer DEFAULT 3::di_src.d CHECK(v>1::di_src.d))",
			"CREATE VIEW di_other.v AS SELECT id,v::di_src.d AS v FROM di_other.t")
		run(fixture.Exec("INSERT INTO di_other.t VALUES(1,12.345,11,4,'[0:1]={5,NULL}','(6,17)'),(2,NULL,12,NULL,NULL,NULL)", 2))
		oid := domainAlterOID(t, p, c, "SELECT 'di_src.d'::regtype::oid")
		array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='di_src.d'::regtype::oid")
		check := domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='di_src.d'::regtype::oid")
		child := domainAlterOID(t, p, c, "SELECT 'di_other.child'::regtype::oid")
		owner := domainAlterOID(t, p, c, "SELECT typowner FROM pg_type WHERE oid='di_other.child'::regtype::oid")
		domainIdentityDDL(t, p, c, r, `ALTER DOMAIN di_src.d RENAME TO "Renamed Domain"`, `ALTER DOMAIN di_src."Renamed Domain" SET SCHEMA di_dst`)
		current := `'di_dst."Renamed Domain"'::regtype::oid`
		domainIdentitySameOID(t, p, c, "SELECT "+current, oid)
		domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid="+current, array)
		domainIdentitySameOID(t, p, c, "SELECT typelem FROM pg_type WHERE oid=(SELECT typarray FROM pg_type WHERE oid="+current+")", oid)
		domainIdentitySameOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid="+current, check)
		domainIdentitySameOID(t, p, c, "SELECT atttypid FROM pg_attribute WHERE attrelid='di_other.t'::regclass::oid AND attname='v'", oid)
		domainIdentitySameOID(t, p, c, "SELECT typbasetype FROM pg_type WHERE oid='di_other.child'::regtype::oid", oid)
		domainIdentitySameOID(t, p, c, "SELECT 'di_other.child'::regtype::oid", child)
		domainIdentitySameOID(t, p, c, "SELECT typowner FROM pg_type WHERE oid='di_other.child'::regtype::oid", owner)
		run(fixture.Select("SELECT t.typname,n.nspname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE t.oid=(SELECT typarray FROM pg_type WHERE oid="+current+")", []string{"typname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"_Renamed Domain", "di_dst"}}))
		run(fixture.Select("SELECT c.conname,n.nspname FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE c.contypid="+current, []string{"conname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"positive", "di_dst"}}))
		run(fixture.Select("SELECT id,v,keep,c,a::text,box::text FROM di_other.t ORDER BY id", []string{"id", "v", "keep", "c", "a", "box"}, []string{"INT4", "NUMERIC", "INT4", "NUMERIC", "TEXT", "TEXT"}, [][]any{{int64(1), "12.35", int64(11), "4.00", "[0:1]={5.00,NULL}", "(6.00,17)"}, {int64(2), nil, int64(12), nil, nil, nil}}))
		run(fixture.Select("SELECT id,v FROM di_other.v ORDER BY id", []string{"id", "v"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(1), "12.35"}, {int64(2), nil}}))
		run(fixture.Exec("INSERT INTO di_other.recipes DEFAULT VALUES", 1))
		run(fixture.Select("SELECT v FROM di_other.recipes", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
		want := fixture.ControlsReferenceDiagnostic("23514", `value for domain di_dst."Renamed Domain" violates check constraint "positive"`)
		want.Schema, want.DataType, want.Constraint = "di_dst", "Renamed Domain", "positive"
		run(fixture.ErrorDiagnostic("INSERT INTO di_other.t(id,v) VALUES(3,0)", want))
		run(fixture.Error("INSERT INTO di_other.t(id,v) VALUES(3,1000)", "22003"))
		run(fixture.Exec("INSERT INTO di_other.t(id) VALUES(3)", 1))
		run(fixture.Select("SELECT v FROM di_other.t WHERE id=3", []string{"v"}, []string{"NUMERIC"}, [][]any{{"1.25"}}))
		// The moved companion and CHECK must not leave phantom source dependencies.
		run(fixture.Exec("DROP SCHEMA di_src RESTRICT", 0))
		run(fixture.Error("DROP SCHEMA di_dst RESTRICT", "2BP01"))
		// NOT VALID history must not be scanned by a pure identity edit.
		domainIdentityDDL(t, p, c, r, "CREATE DOMAIN di_other.history AS integer", "CREATE TABLE di_other.old(v di_other.history)")
		run(fixture.Exec("INSERT INTO di_other.old VALUES(-1)", 1))
		domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_other.history ADD CONSTRAINT later CHECK(VALUE>0) NOT VALID", "ALTER DOMAIN di_other.history RENAME TO historical", "ALTER DOMAIN di_other.historical SET SCHEMA di_dst")
		run(fixture.Select("SELECT v FROM di_other.old", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}}))
		run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE contypid='di_dst.historical'::regtype::oid", []string{"convalidated"}, []string{"BOOL"}, [][]any{{false}}))
		domainIdentityDDL(t, p, c, r, "CREATE DOMAIN di_other.required AS integer NOT NULL", "CREATE DOMAIN di_other.inherited AS di_other.required", "ALTER DOMAIN di_other.required RENAME TO required_new", "ALTER DOMAIN di_other.required_new SET SCHEMA di_dst")
		run(fixture.Error("SELECT NULL::di_dst.required_new", "23502"))
		run(fixture.Error("SELECT NULL::di_other.inherited", "23502"))
	})
}

func TestReferenceDomainIdentityDiagnostics(t *testing.T) {
	domainIdentityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		domainIdentityDDL(t, p, c, r, "CREATE SCHEMA di_src", "CREATE SCHEMA di_dst", "CREATE DOMAIN di_src.d AS integer", "CREATE DOMAIN di_src.occupied AS integer", "CREATE DOMAIN di_dst.d AS integer")
		domainIdentityDDL(t, p, c, r, "CREATE TABLE di_src.t(v di_src.d,keep integer)")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO di_src.t VALUES(7,11)", 1))
		cases := []struct{ q, state, message string }{
			{"ALTER DOMAIN di_src.absent RENAME TO x", "42704", `type "di_src.absent" does not exist`},
			{"ALTER DOMAIN di_missing.d SET SCHEMA di_dst", "3F000", `schema "di_missing" does not exist`},
			{"ALTER DOMAIN pg_catalog.int4 RENAME TO x", "42809", "integer is not a domain"},
			{"DROP DOMAIN IF EXISTS pg_catalog.int4", "42809", `"pg_catalog.int4" is not a domain`},
			{"ALTER DOMAIN di_src.d RENAME TO d", "42710", `type "d" already exists`},
			{"ALTER DOMAIN di_src.d RENAME TO occupied", "42710", `type "occupied" already exists`},
			{"ALTER DOMAIN di_src.d SET SCHEMA di_dst", "42710", `type "d" already exists in schema "di_dst"`},
			{"ALTER DOMAIN di_src.d OWNER TO di_absent_role", "42704", `role "di_absent_role" does not exist`},
			{"ALTER DOMAIN di_src.d SET SCHEMA di_missing", "3F000", `schema "di_missing" does not exist`},
			{"ALTER DOMAIN di_src.d SET SCHEMA pg_toast", "0A000", "cannot move objects into or out of TOAST schema"},
		}
		for _, tc := range cases {
			for _, prepared := range []bool{false, true} {
				domainIdentityError(t, p, c, tc.q, tc.state, tc.message, prepared)
			}
		}
		// Move cannot displace a destination companion even if the base name is free.
		domainIdentityDDL(t, p, c, r, "DROP DOMAIN di_dst.d", "CREATE DOMAIN di_dst._d AS integer")
		domainIdentityError(t, p, c, "ALTER DOMAIN di_src.d SET SCHEMA di_dst", "42710", `type "_d" already exists in schema "di_dst"`, false)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT v,keep FROM di_src.t", []string{"v", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(11)}}))
		// Rename CAN displace automatic arrays, including its own array.
		oid := domainAlterOID(t, p, c, "SELECT 'di_src.d'::regtype::oid")
		array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='di_src.d'::regtype::oid")
		domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_src.d RENAME TO _d")
		domainIdentitySameOID(t, p, c, "SELECT 'di_src._d'::regtype::oid", oid)
		domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='di_src._d'::regtype::oid", array)
		domainIdentityDDL(t, p, c, r, "CREATE DOMAIN di_src.foreign_array AS integer", "ALTER DOMAIN di_src._d RENAME TO _foreign_array")
		domainIdentitySameOID(t, p, c, "SELECT 'di_src._foreign_array'::regtype::oid", oid)
		// Pinned18.6 clips multibyte names and chooses a numeric suffix when
		// the first underscore candidate is occupied.
		longName := strings.Repeat("é", 31) + "x"
		domainIdentityDDL(t, p, c, r, `CREATE DOMAIN di_src."_`+strings.Repeat("é", 31)+`" AS integer`, `ALTER DOMAIN di_src._foreign_array RENAME TO "`+longName+`"`)
		domainIdentitySameOID(t, p, c, `SELECT 'di_src."`+longName+`"'::regtype::oid`, oid)
		domainIdentitySameOID(t, p, c, `SELECT typarray FROM pg_type WHERE oid='di_src."`+longName+`"'::regtype::oid`, array)
		var clipped string
		if err := c.QueryRowContext(p.Ctx, `SELECT typname FROM pg_type WHERE oid=(SELECT typarray FROM pg_type WHERE oid='di_src."`+longName+`"'::regtype::oid)`).Scan(&clipped); err != nil {
			t.Fatal(err)
		}
		if clipped != "_"+strings.Repeat("é", 30)+"_1" {
			t.Fatalf("clipped companion=%q", clipped)
		}
		domainIdentityDDL(t, p, c, r, `ALTER DOMAIN di_src."`+longName+`" RENAME TO _foreign_array`)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='di_src'::regnamespace::oid ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{
			{"__foreign_array"}, {"__foreign_array_1"}, {"__" + strings.Repeat("é", 30)}, {"_foreign_array"}, {"_occupied"}, {"_t"}, {"_" + strings.Repeat("é", 31)}, {"foreign_array"}, {"occupied"}, {"t"},
		}))
		domainIdentityDDL(t, p, c, r, "CREATE TEMP TABLE identity_temp(v integer)")
		domainIdentityError(t, p, c, "ALTER DOMAIN di_src._foreign_array SET SCHEMA pg_temp", "0A000", "cannot move objects into or out of temporary schemas", false)
		// Syntax positions count Unicode characters, not UTF8 bytes.
		want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "extra"`)
		want.Position = "50"
		s := fixture.Prepared(fixture.ErrorDiagnostic(`ALTER DOMAIN di_src._foreign_array RENAME TO "é" extra`, want))
		s.ErrorStage = "prepare"
		fixture.RunStep(t, p.Ctx, c, r, s)
	})
}
