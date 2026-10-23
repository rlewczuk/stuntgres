package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeLifecycleBatchCompanions(t *testing.T) {
	for _, family := range []struct {
		name, create, value string
		displaced           bool
	}{
		{"enum", "CREATE TYPE tl_src.t AS ENUM ('a','b')", "a", false},
		{"domain", "CREATE DOMAIN tl_src.t AS integer", "7", false},
		{"composite", "CREATE TYPE tl_src.t AS(n integer,note text)", "(7,x)", false},
		{"displaced_enum", "CREATE TYPE tl_src.t AS ENUM ('a','b')", "a", true},
	} {
		for _, arrayFirst := range []bool{false, true} {
			for _, mode := range []struct{ name, suffix string }{{"default", ""}, {"restrict", " RESTRICT"}, {"cascade", " CASCADE"}} {
				for _, duplicates := range []bool{false, true} {
					for _, dependent := range []bool{false, true} {
						name := fmt.Sprintf("%s/array_first=%t/%s/duplicates=%t/column=%t", family.name, arrayFirst, mode.name, duplicates, dependent)
						t.Run(name, func(t *testing.T) {
							typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
								domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_src", "CREATE TYPE tl_src.peer AS ENUM ('peer')")
								peer := domainAlterOID(t, p, c, "SELECT 'tl_src.peer'::regtype::oid")
								for _, prepared := range []bool{false, true} {
									for _, queryMode := range []bool{false, true} {
										t.Run(fmt.Sprintf("prepared=%t/query=%t", prepared, queryMode), func(t *testing.T) {
											run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
											domainIdentityDDL(t, p, c, r, family.create)
											oid := domainAlterOID(t, p, c, "SELECT 'tl_src.t'::regtype::oid")
											array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_src.t'::regtype::oid")
											var shadow, shadowArray, backing int64
											if family.displaced {
												domainIdentityDDL(t, p, c, r, "CREATE TYPE tl_src._t AS ENUM ('shadow')")
												shadow = domainAlterOID(t, p, c, "SELECT 'tl_src._t'::regtype::oid")
												shadowArray, _, _ = typeLifecycleCompanion(t, p, c, "tl_src._t")
											}
											actualArray, arrayName, arraySQL := typeLifecycleCompanion(t, p, c, "tl_src.t")
											wasDisplaced := arrayName != "_t"
											if actualArray != array || wasDisplaced != family.displaced {
												t.Fatal("companion displacement/identity precondition", array, actualArray, arrayName)
											}
											oidQueryArgument(t, p, c, array, oid)
											if family.name == "composite" {
												backing = domainAlterOID(t, p, c, "SELECT typrelid::oid FROM pg_type WHERE oid='tl_src.t'::regtype::oid")
											}
											declaration := "CREATE TABLE tl_src.holder(keep integer)"
											if dependent {
												declaration = "CREATE TABLE tl_src.holder(keep integer,v tl_src.t)"
											}
											domainIdentityDDL(t, p, c, r, declaration)
											holder := domainAlterOID(t, p, c, "SELECT 'tl_src.holder'::regclass::oid")
											if dependent {
												run(fixture.Exec("INSERT INTO tl_src.holder VALUES(11,$1),(12,NULL)", 2, family.value))
											} else {
												run(fixture.Exec("INSERT INTO tl_src.holder VALUES(11),(12)", 2))
											}
											roots := []string{"tl_src.t", arraySQL}
											if arrayFirst {
												roots[0], roots[1] = roots[1], roots[0]
											}
											if duplicates {
												roots = append(roots, roots...)
											}
											q := "DROP TYPE " + strings.Join(roots, ",") + mode.suffix
											if dependent && mode.name != "cascade" {
												want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop desired object(s) because other objects depend on them")
												want.Detail = "column v of table tl_src.holder depends on type tl_src.t"
												want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
												*notices = nil
												s := fixture.ErrorDiagnostic(q, want)
												s.Prepared, s.SelectRows, s.ErrorStage = prepared, queryMode, "execute"
												run(s)
												domainIdentityNotices(t, notices)
												domainIdentitySameOID(t, p, c, "SELECT 'tl_src.t'::regtype::oid", oid)
												domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_src.t'::regtype::oid", array)
												oidQueryArgument(t, p, c, array, oid)
												run(fixture.Select("SELECT typname FROM pg_type WHERE oid=$1::oid OR oid=$2::oid ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{{arrayName}, {"t"}}, oid, array))
												run(fixture.Select("SELECT keep,v::text AS v FROM tl_src.holder ORDER BY keep", []string{"keep", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(11), family.value}, {int64(12), nil}}))
												if backing != 0 {
													typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_class WHERE oid=$1::oid", 1, backing)
												}
												domainIdentityDDL(t, p, c, r, "ALTER TABLE tl_src.holder DROP COLUMN v")
											}
											*notices = nil
											s := fixture.Exec(q, 0)
											s.Prepared, s.SelectRows = prepared, queryMode
											run(s)
											if dependent && mode.name == "cascade" {
												domainIdentityNotices(t, notices, typeLifecycleNotice("drop cascades to column v of table tl_src.holder"))
											} else {
												domainIdentityNotices(t, notices)
											}
											typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE oid=$1::oid OR oid=$2::oid", 0, oid, array)
											if backing != 0 {
												typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_class WHERE oid=$1::oid", 0, backing)
											}
											domainIdentitySameOID(t, p, c, "SELECT 'tl_src.holder'::regclass::oid", holder)
											run(fixture.Select("SELECT keep FROM tl_src.holder ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}}))
											run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid=$1::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"keep"}}, holder))
											domainIdentitySameOID(t, p, c, "SELECT 'tl_src.peer'::regtype::oid", peer)
											run(fixture.Select("SELECT 'peer'::tl_src.peer::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"peer"}}))
											if shadow != 0 {
												domainIdentitySameOID(t, p, c, "SELECT 'tl_src._t'::regtype::oid", shadow)
												domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_src._t'::regtype::oid", shadowArray)
												oidQueryArgument(t, p, c, shadowArray, shadow)
												run(fixture.Select("SELECT 'shadow'::tl_src._t::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"shadow"}}))
											}
											domainIdentityDDL(t, p, c, r, "DROP TABLE tl_src.holder")
											if shadow != 0 {
												domainIdentityDDL(t, p, c, r, "DROP TYPE tl_src._t")
											}
										})
									}
								}
							})
						})
					}
				}
			}
		}
	}
	for _, family := range []struct{ extension, typ, input, output string }{
		{"cube", "cube", "(1,2)", "(1, 2)"}, {"intarray", "query_int", "1", "1"}, {"citext", "citext", "MiXeD", "MiXeD"}, {"earthdistance", "earth", "(6378168,0,0)", "(6378168, 0, 0)"},
	} {
		for _, arrayFirst := range []bool{false, true} {
			for _, mode := range []string{"", " RESTRICT", " CASCADE"} {
				for _, duplicates := range []bool{false, true} {
					t.Run(fmt.Sprintf("installed_%s/array_first=%t/mode=%s/duplicates=%t", family.typ, arrayFirst, mode, duplicates), func(t *testing.T) {
						typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
							domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_src")
							if family.extension == "earthdistance" {
								domainIdentityDDL(t, p, c, r, "CREATE EXTENSION cube WITH SCHEMA tl_src")
							}
							domainIdentityDDL(t, p, c, r, "CREATE EXTENSION "+family.extension+" WITH SCHEMA tl_src", "CREATE TABLE tl_src.holder(keep integer,v tl_src."+family.typ+")")
							run(fixture.Exec("INSERT INTO tl_src.holder VALUES(11,$1),(12,NULL)", 2, family.input))
							base := "tl_src." + family.typ
							oid := domainAlterOID(t, p, c, "SELECT '"+base+"'::regtype::oid")
							array, _, arraySQL := typeLifecycleCompanion(t, p, c, base)
							ext := domainAlterOID(t, p, c, "SELECT oid FROM pg_extension WHERE extname='"+family.extension+"'")
							roots := []string{base, arraySQL}
							if arrayFirst {
								roots[0], roots[1] = roots[1], roots[0]
							}
							if duplicates {
								roots = append(roots, roots...)
							}
							q := "DROP TYPE " + strings.Join(roots, ",") + mode
							// A pending internal owner defers the companion; that genuine
							// element must still face its own extension's protection.
							want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type "+base+" because extension "+family.extension+" requires it")
							want.Hint = "You can drop extension " + family.extension + " instead."
							for _, prepared := range []bool{false, true} {
								for _, queryMode := range []bool{false, true} {
									*notices = nil
									s := fixture.ErrorDiagnostic(q, want)
									s.Prepared, s.SelectRows, s.ErrorStage = prepared, queryMode, "execute"
									run(s)
									domainIdentityNotices(t, notices)
									domainIdentitySameOID(t, p, c, "SELECT '"+base+"'::regtype::oid", oid)
									domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='"+base+"'::regtype::oid", array)
									oidQueryArgument(t, p, c, array, oid)
									typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_extension WHERE oid=$1::oid", 1, ext)
									run(fixture.Select("SELECT keep,v::text AS v FROM tl_src.holder ORDER BY keep", []string{"keep", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(11), family.output}, {int64(12), nil}}))
								}
							}
						})
					})
				}
			}
		}
	}
}
