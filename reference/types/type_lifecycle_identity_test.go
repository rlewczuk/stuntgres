package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeLifecycleIdentity(t *testing.T) {
	for _, family := range []struct{ name, create, value, array, metadata string }{
		{"enum", "CREATE TYPE tl_src.t AS ENUM ('z','a')", "z", "", ""},
		{"domain", "CREATE DOMAIN tl_src.t AS integer DEFAULT 7 CONSTRAINT positive CHECK(VALUE>0)", "7", "[0:1]={7,NULL}", "INT4"},
		{"composite", "CREATE TYPE tl_src.t AS (n integer,note text)", "(7,x)", "{\"(7,x)\",NULL}", ""},
	} {
		t.Run(family.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"tl_successor"})
			typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_src", "CREATE SCHEMA tl_dst", "CREATE SCHEMA tl_other", "CREATE ROLE tl_successor")
				// These actual utilities are prepared before the source exists.
				renameQ, moveQ := "ALTER TYPE tl_src.t RENAME TO renamed", "ALTER TYPE tl_src.renamed SET SCHEMA tl_dst"
				rename := typeLifecycleHeld(t, p, c, renameQ)
				move := typeLifecycleHeld(t, p, c, moveQ)
				domainIdentityDDL(t, p, c, r, family.create, strings.ReplaceAll(family.create, "tl_src.t", "tl_other.t"),
					"CREATE TABLE tl_other.holder(id integer,keep integer,v tl_src.t)",
					"CREATE VIEW tl_other.live AS SELECT id,v FROM tl_other.holder")
				run(fixture.Exec("INSERT INTO tl_other.holder VALUES(1,11,$1),(2,12,NULL)", 2, family.value))
				if family.array != "" {
					domainIdentityDDL(t, p, c, r, "CREATE TABLE tl_other.arrays(v tl_src.t[])")
					run(fixture.Exec("INSERT INTO tl_other.arrays VALUES($1),(NULL)", 2, family.array))
				}
				oid := domainAlterOID(t, p, c, "SELECT 'tl_src.t'::regtype::oid")
				array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_src.t'::regtype::oid")
				peer := domainAlterOID(t, p, c, "SELECT 'tl_other.t'::regtype::oid")
				if oid == peer {
					t.Fatal("same-spelling peer shares identity")
				}
				var rel, member int64
				if family.name == "composite" {
					rel = domainAlterOID(t, p, c, "SELECT typrelid FROM pg_type WHERE oid='tl_src.t'::regtype::oid")
				}
				if family.name == "enum" {
					member = domainAlterOID(t, p, c, "SELECT oid FROM pg_enum WHERE enumtypid='tl_src.t'::regtype::oid AND enumlabel='z'")
				}
				fixture.RunStep(t, p.Ctx, rename, r, fixture.Exec(renameQ, 0))
				fixture.RunStep(t, p.Ctx, move, r, fixture.Exec(moveQ, 0))
				run(fixture.Prepared(fixture.Exec("ALTER TYPE tl_dst.renamed OWNER TO tl_successor", 0)))
				domainIdentitySameOID(t, p, c, "SELECT 'tl_dst.renamed'::regtype::oid", oid)
				domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_dst.renamed'::regtype::oid", array)
				oidQueryArgument(t, p, c, array, oid)
				domainIdentitySameOID(t, p, c, "SELECT 'tl_other.t'::regtype::oid", peer)
				run(fixture.Select("SELECT t.typname,n.nspname,pg_get_userbyid(t.typowner) AS owner FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE t.oid=$1::oid", []string{"typname", "nspname", "owner"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"_renamed", "tl_dst", "tl_successor"}}, array))
				rows := [][]any{{int64(1), int64(11), family.value}, {int64(2), int64(12), nil}}
				if family.name == "domain" {
					rows[0][2] = int64(7)
				}
				s := fixture.Select("SELECT id,keep,v FROM tl_other.holder ORDER BY id", []string{"id", "keep", "v"}, []string{"INT4", "INT4", family.metadata}, rows)
				if family.name != "domain" {
					s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
						if column != 2 || v == nil {
							return false
						}
						if b.PG {
							if _, ok := v.([]byte); !ok {
								t.Fatalf("raw custom cell=%T", v)
							}
						} else if _, ok := v.(string); !ok {
							t.Fatalf("raw custom cell=%T", v)
						}
						return true
					}
				}
				run(s)
				run(fixture.Prepared(s))
				run(fixture.Select("SELECT id,v::text AS v FROM tl_other.live ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), family.value}, {int64(2), nil}}))
				if rel != 0 {
					domainIdentitySameOID(t, p, c, "SELECT typrelid FROM pg_type WHERE oid='tl_dst.renamed'::regtype::oid", rel)
					run(fixture.Select("SELECT relname,relkind,reltype='tl_dst.renamed'::regtype::oid AS reciprocal,pg_get_userbyid(relowner) AS owner FROM pg_class WHERE oid=$1::oid", []string{"relname", "relkind", "reciprocal", "owner"}, []string{"NAME", "CHAR", "BOOL", "NAME"}, [][]any{{"renamed", "c", true, "tl_successor"}}, rel))
					run(fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid=$1::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"n", int64(1)}, {"note", int64(2)}}, rel))
				}
				if member != 0 {
					domainIdentitySameOID(t, p, c, "SELECT oid FROM pg_enum WHERE enumtypid='tl_dst.renamed'::regtype::oid AND enumlabel='z'", member)
					run(fixture.Select("SELECT enumlabel FROM pg_enum WHERE enumtypid='tl_dst.renamed'::regtype::oid ORDER BY enumsortorder", []string{"enumlabel"}, []string{"NAME"}, [][]any{{"z"}, {"a"}}))
					run(fixture.Exec("INSERT INTO tl_other.holder VALUES(3,13,'a')", 1))
					run(fixture.Select("SELECT v::text AS v FROM tl_other.holder ORDER BY holder.v", []string{"v"}, []string{"TEXT"}, [][]any{{"z"}, {"a"}, {nil}}))
				}
				if family.array != "" {
					run(fixture.Select("SELECT v::text AS v FROM tl_other.arrays ORDER BY v NULLS LAST", []string{"v"}, []string{"TEXT"}, [][]any{{family.array}, {nil}}))
				}
				if family.name == "domain" {
					domainIdentityDDL(t, p, c, r, "CREATE DOMAIN tl_other.child AS tl_dst.renamed", "CREATE TABLE tl_other.defaults(v tl_other.child)")
					run(fixture.Exec("INSERT INTO tl_other.defaults DEFAULT VALUES", 1))
					run(fixture.Select("SELECT v FROM tl_other.defaults", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					want := fixture.ControlsReferenceDiagnostic("23514", `value for domain tl_other.child violates check constraint "positive"`)
					want.Schema, want.DataType, want.Constraint = "tl_other", "child", "positive"
					run(fixture.ErrorDiagnostic("INSERT INTO tl_other.defaults VALUES(0)", want))
				}
				typeLifecycleFailure(t, p, c, "ALTER TYPE tl_src.t RENAME TO absent", fixture.ControlsReferenceDiagnostic("42704", `type "tl_src.t" does not exist`))
				duplicate := fixture.ControlsReferenceDiagnostic("42710", `type "renamed" already exists`)
				if family.name == "composite" {
					duplicate = fixture.ControlsReferenceDiagnostic("42P07", `relation "renamed" already exists`)
				}
				typeLifecycleFailure(t, p, c, "ALTER TYPE tl_dst.renamed RENAME TO renamed", duplicate)
				run(fixture.Exec("ALTER TYPE tl_dst.renamed SET SCHEMA tl_dst", 0))
			})
		})
	}
	t.Run("generated_companions_and_collisions", func(t *testing.T) {
		typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_src", "CREATE SCHEMA tl_dst", "CREATE TYPE tl_src.t AS ENUM ('z')", "CREATE TYPE tl_src.foreign_array AS ENUM ()", "CREATE TYPE tl_dst._t AS ENUM ()")
			oid := domainAlterOID(t, p, c, "SELECT 'tl_src.t'::regtype::oid")
			array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_src.t'::regtype::oid")
			typeLifecycleFailure(t, p, c, "ALTER TYPE tl_src.t SET SCHEMA tl_dst", fixture.ControlsReferenceDiagnostic("42710", `type "_t" already exists in schema "tl_dst"`))
			domainIdentitySameOID(t, p, c, "SELECT 'tl_src.t'::regtype::oid", oid)
			domainIdentityDDL(t, p, c, r, "ALTER TYPE tl_src.t RENAME TO _t", "ALTER TYPE tl_src._t RENAME TO _foreign_array")
			longName := strings.Repeat("é", 31) + "x"
			domainIdentityDDL(t, p, c, r, `CREATE TYPE tl_src."_`+strings.Repeat("é", 31)+`" AS ENUM ()`, `ALTER TYPE tl_src._foreign_array RENAME TO "`+longName+`"`)
			domainIdentitySameOID(t, p, c, `SELECT 'tl_src."`+longName+`"'::regtype::oid`, oid)
			domainIdentitySameOID(t, p, c, `SELECT typarray FROM pg_type WHERE oid='tl_src."`+longName+`"'::regtype::oid`, array)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT typname FROM pg_type WHERE oid=$1::oid", []string{"typname"}, []string{"NAME"}, [][]any{{"_" + strings.Repeat("é", 30) + "_1"}}, array))
		})
	})
	t.Run("diagnostics_and_complete_input", func(t *testing.T) {
		typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_src", "CREATE SCHEMA tl_dst", "CREATE TYPE tl_src.t AS ENUM ('z')", "CREATE TYPE tl_src.taken AS ENUM ()", "CREATE TYPE tl_dst.t AS ENUM ()")
			for _, tc := range []struct{ q, state, message string }{
				{"ALTER TYPE tl_src.absent RENAME TO x", "42704", `type "tl_src.absent" does not exist`},
				{"ALTER TYPE tl_missing.t SET SCHEMA tl_dst", "3F000", `schema "tl_missing" does not exist`},
				{"ALTER TYPE tl_src.t SET SCHEMA tl_missing", "3F000", `schema "tl_missing" does not exist`},
				{"ALTER TYPE tl_src.t RENAME TO taken", "42710", `type "taken" already exists`},
				{"ALTER TYPE tl_src.t SET SCHEMA tl_dst", "42710", `type "t" already exists in schema "tl_dst"`},
				{"ALTER TYPE tl_src.t SET SCHEMA pg_toast", "0A000", "cannot move objects into or out of TOAST schema"},
			} {
				typeLifecycleFailure(t, p, c, tc.q, fixture.ControlsReferenceDiagnostic(tc.state, tc.message))
			}
			domainIdentityDDL(t, p, c, r, "CREATE TEMP TABLE tl_temp(v integer)")
			typeLifecycleFailure(t, p, c, "ALTER TYPE tl_src.t SET SCHEMA pg_temp", fixture.ControlsReferenceDiagnostic("0A000", "cannot move objects into or out of temporary schemas"))
			q := `ALTER TYPE tl_src.t RENAME TO "é" extra`
			want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "extra"`)
			want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "extra")]) + 1)
			fixture.CompositeReferenceError(t, p, c, q, "prepare", want)
			typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE typnamespace='tl_src'::regnamespace::oid AND typname='t'", 1)
			typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE typnamespace='tl_src'::regnamespace::oid AND typname='é'", 0)
		})
	})
}

// Read by explicit OID, not a guessed underscore spelling.
func oidQueryArgument(t *testing.T, p *fixture.Pair, c *sql.Conn, array, want int64) int64 {
	t.Helper()
	var got int64
	if err := c.QueryRowContext(p.Ctx, "SELECT typelem FROM pg_type WHERE oid=$1::oid", array).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("array element=%d want %d", got, want)
	}
	return want
}
