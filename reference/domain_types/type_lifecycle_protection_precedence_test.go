package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeLifecycleProtectionPrecedence(t *testing.T) {
	for _, target := range []string{"enum_array", "table_rowtype", "table_array"} {
		for _, owner := range []bool{false, true} {
			for _, action := range []string{"rename", "schema_missing", "schema_denied", "schema_allowed", "schema_occupied"} {
				if action == "schema_occupied" && (target != "table_rowtype" || !owner) {
					continue
				}
				t.Run(fmt.Sprintf("%s/owner=%t/%s", target, owner, action), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"tl_guard_owner", "tl_guard_other"})
					typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
						domainIdentityDDL(t, p, c, r, "CREATE ROLE tl_guard_owner", "CREATE ROLE tl_guard_other",
							"CREATE SCHEMA tl_src", "CREATE SCHEMA tl_dst", "CREATE SCHEMA tl_other",
							"GRANT USAGE ON SCHEMA tl_src,tl_dst,tl_other TO PUBLIC",
							"GRANT CREATE ON SCHEMA tl_src TO tl_guard_owner",
							"GRANT CREATE ON SCHEMA tl_other TO tl_guard_owner,tl_guard_other", "SET ROLE tl_guard_owner")
						base, baseName := "tl_src.t", "t"
						if target == "enum_array" {
							domainIdentityDDL(t, p, c, r, "CREATE TYPE tl_src.t AS ENUM ('a','b')")
						} else {
							base, baseName = "tl_src.tl_row", "tl_row"
							domainIdentityDDL(t, p, c, r, "CREATE TABLE tl_src.tl_row(keep integer,v integer)")
							run(fixture.Exec("INSERT INTO tl_src.tl_row VALUES(11,7),(12,NULL)", 2))
						}
						domainIdentityDDL(t, p, c, r, "RESET ROLE")
						var destinationOID, destinationArray, destinationBacking int64
						if action == "schema_occupied" {
							domainIdentityDDL(t, p, c, r, "CREATE TYPE tl_other.tl_row AS ENUM ('a')",
								"CREATE TABLE public.tl_destination_values(v tl_other.tl_row)")
							run(fixture.Exec("INSERT INTO public.tl_destination_values VALUES('a')", 1))
							destinationOID = domainAlterOID(t, p, c, "SELECT 'tl_other.tl_row'::regtype::oid")
							destinationArray, _, _ = typeLifecycleCompanion(t, p, c, "tl_other.tl_row")
							destinationBacking = domainAlterOID(t, p, c, "SELECT 'public.tl_destination_values'::regclass::oid")
						}
						oid := domainAlterOID(t, p, c, "SELECT '"+base+"'::regtype::oid")
						array, arrayName, arraySQL := typeLifecycleCompanion(t, p, c, base)
						var backing int64
						if target != "enum_array" {
							backing = domainAlterOID(t, p, c, "SELECT 'tl_src.tl_row'::regclass::oid")
						}
						targetSQL, targetDisplay, targetOID := arraySQL, base+"[]", array
						if target == "table_rowtype" {
							targetSQL, targetDisplay, targetOID = base, base, oid
						}
						actor := "tl_guard_other"
						if owner {
							actor = "tl_guard_owner"
						}
						q := "ALTER TYPE " + targetSQL + " RENAME TO forbidden"
						want := fixture.ControlsReferenceDiagnostic("42501", "must be owner of type "+base)
						switch action {
						case "schema_missing":
							q = "ALTER TYPE " + targetSQL + " SET SCHEMA tl_missing"
							want = fixture.ControlsReferenceDiagnostic("3F000", `schema "tl_missing" does not exist`)
						case "schema_denied":
							q = "ALTER TYPE " + targetSQL + " SET SCHEMA tl_dst"
							want = fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema tl_dst")
						case "schema_allowed":
							q = "ALTER TYPE " + targetSQL + " SET SCHEMA tl_other"
						case "schema_occupied":
							q = "ALTER TYPE " + targetSQL + " SET SCHEMA tl_other"
							want = fixture.ControlsReferenceDiagnostic("42710", `type "tl_row" already exists in schema "tl_other"`)
						}
						if owner && (action == "rename" || action == "schema_allowed") {
							if target == "table_rowtype" {
								want = fixture.ControlsReferenceDiagnostic("42809", targetDisplay+" is a table's row type")
								want.Hint = "Use ALTER TABLE instead."
							} else {
								want = fixture.ControlsReferenceDiagnostic("42809", "cannot alter array type "+targetDisplay)
								want.Hint = "You can alter type " + base + ", which will alter the array type as well."
							}
						}
						check := func() {
							t.Helper()
							domainIdentitySameOID(t, p, c, "SELECT '"+base+"'::regtype::oid", oid)
							domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='"+base+"'::regtype::oid", array)
							domainIdentitySameOID(t, p, c, "SELECT '"+targetSQL+"'::regtype::oid", targetOID)
							oidQueryArgument(t, p, c, array, oid)
							run(fixture.Select("SELECT t.typname,n.nspname,pg_get_userbyid(t.typowner) AS owner FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE t.oid=$1::oid OR t.oid=$2::oid ORDER BY t.typname", []string{"typname", "nspname", "owner"}, []string{"NAME", "NAME", "NAME"}, [][]any{{arrayName, "tl_src", "tl_guard_owner"}, {baseName, "tl_src", "tl_guard_owner"}}, oid, array))
							typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE typnamespace='tl_src'::regnamespace::oid", 2)
							var destinationCount int64
							if destinationOID != 0 {
								destinationCount = 2
								domainIdentitySameOID(t, p, c, "SELECT 'tl_other.tl_row'::regtype::oid", destinationOID)
								domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_other.tl_row'::regtype::oid", destinationArray)
								oidQueryArgument(t, p, c, destinationArray, destinationOID)
								domainIdentitySameOID(t, p, c, "SELECT 'public.tl_destination_values'::regclass::oid", destinationBacking)
								bootstrapOwner := "postgres"
								if backend.PG {
									bootstrapOwner = "stuntgres"
								}
								run(fixture.Select("SELECT t.typname,n.nspname,pg_get_userbyid(t.typowner) AS owner FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE t.oid=$1::oid", []string{"typname", "nspname", "owner"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"tl_row", "tl_other", bootstrapOwner}}, destinationOID))
								run(fixture.Select("SELECT enumlabel FROM pg_enum WHERE enumtypid=$1::oid ORDER BY enumsortorder", []string{"enumlabel"}, []string{"NAME"}, [][]any{{"a"}}, destinationOID))
								run(fixture.Select("SELECT v::text AS v FROM public.tl_destination_values", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}}))
							}
							typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE typnamespace='tl_dst'::regnamespace::oid OR typnamespace='tl_other'::regnamespace::oid", destinationCount)
							typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_class WHERE relnamespace='tl_dst'::regnamespace::oid OR relnamespace='tl_other'::regnamespace::oid", 0)
							if backing != 0 {
								domainIdentitySameOID(t, p, c, "SELECT 'tl_src.tl_row'::regclass::oid", backing)
								run(fixture.Select("SELECT r.relname,n.nspname,pg_get_userbyid(r.relowner) AS owner,r.reltype=$2::oid AS same_type FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE r.oid=$1::oid", []string{"relname", "nspname", "owner", "same_type"}, []string{"NAME", "NAME", "NAME", "BOOL"}, [][]any{{"tl_row", "tl_src", "tl_guard_owner", true}}, backing, oid))
								run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid=$1::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"keep"}, {"v"}}, backing))
								run(fixture.Select("SELECT keep,v FROM tl_src.tl_row ORDER BY keep", []string{"keep", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(7)}, {int64(12), nil}}))
							} else {
								run(fixture.Select("SELECT enumlabel FROM pg_enum WHERE enumtypid=$1::oid ORDER BY enumsortorder", []string{"enumlabel"}, []string{"NAME"}, [][]any{{"a"}, {"b"}}, oid))
							}
						}
						check()
						for _, prepared := range []bool{false, true} {
							for _, queryMode := range []bool{false, true} {
								domainIdentityDDL(t, p, c, r, "SET ROLE "+actor)
								*notices = nil
								s := fixture.ErrorDiagnostic(q, want)
								s.Prepared, s.SelectRows, s.ErrorStage = prepared, queryMode, "execute"
								run(s)
								domainIdentityNotices(t, notices)
								domainIdentityDDL(t, p, c, r, "RESET ROLE")
								check()
							}
						}
					})
				})
			}
		}
	}
}
