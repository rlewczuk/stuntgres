package reference_test

import (
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Membership is not the C implementation's origin. The images and metadata
// checker are the same independent released-layout fixtures as the BRIN suite.
func ownershipPageinspectSurvivors(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "detached"
		if foreign {
			name = "foreign"
		}
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"eo_pi_owner", "eo_pi_acl"})
			fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := fixture.OwnershipRun(t, p, b, c)
				for _, q := range []string{
					`CREATE ROLE eo_pi_owner SUPERUSER`, `CREATE ROLE eo_pi_acl`,
					`CREATE SCHEMA eo_old AUTHORIZATION eo_pi_owner`, `CREATE SCHEMA eo_other`,
					`CREATE EXTENSION pageinspect SCHEMA eo_old`,
					`ALTER FUNCTION eo_old.brin_page_type(bytea) OWNER TO eo_pi_owner`,
					`REVOKE ALL ON FUNCTION eo_old.brin_page_type(bytea) FROM PUBLIC`,
					`GRANT EXECUTE ON FUNCTION eo_old.brin_page_type(bytea) TO eo_pi_acl`,
				} {
					run(fixture.Exec(q, 0))
				}
				id := fixture.OwnershipID(t, p, c, `SELECT 'eo_old.brin_page_type(bytea)'::regprocedure::bigint`)
				// Capture genuine declarations, including OUT arrays of the record
				// routines. No engine descriptor, discovery filter or fabricated rows.
				declarations := `SELECT p.oid::bigint,p.proowner::bigint,p.proacl::text,p.proargtypes::text,p.proallargtypes::text,p.proargmodes::text,p.proargnames::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin FROM pg_proc p WHERE p.pronamespace='eo_old'::regnamespace AND ((p.prosrc IN ('brin_page_type','brin_metapage_info','brin_revmap_data','brin_page_items','fsm_page_contents','gin_metapage_info','gin_page_opaque_info','gin_leafpage_items')) OR (p.proname='bt_page_items' AND p.proargtypes::text='17') OR (p.prosrc='page_header' AND p.proargtypes::text='17') OR (p.prosrc='page_checksum_1_9' AND p.proargtypes::text='17 20')) ORDER BY p.oid`
				baseline := fixture.OwnershipSnapshot(t, p, c, declarations)
				if len(baseline) != 11 {
					t.Fatalf("released represented pageinspect declaration count: %d", len(baseline))
				}
				identity := `SELECT p.oid::bigint,p.pronamespace::bigint,p.proowner::bigint,p.proacl::text,p.proargtypes::text,p.proallargtypes::text,p.proargmodes::text,p.proargnames::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin FROM pg_proc p WHERE p.oid=` + fixture.OwnershipNumber(id)
				before := fixture.OwnershipSnapshot(t, p, c, identity)
				run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=`+fixture.OwnershipNumber(id)+` AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pageinspect') AND deptype='e'`, 1))
				run(fixture.Exec(`ALTER EXTENSION pageinspect DROP FUNCTION eo_old.brin_page_type(bytea)`, 0))
				run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=`+fixture.OwnershipNumber(id)+` AND refclassid='pg_extension'::regclass AND deptype='e'`, 0))
				run(fixture.Exec(`ALTER FUNCTION eo_old.brin_page_type(bytea) RENAME TO eo_classify`, 0))
				values := func(conn *sql.Conn, recorder *fixture.Recorder) {
					destination := b
					destination.Recorder = recorder
					r := fixture.OwnershipRun(t, p, destination, conn)
					for _, tc := range []struct {
						page []byte
						want any
					}{{fixture.BRINMeta(), "meta"}, {fixture.BRINImage(0xf092), "revmap"}, {fixture.BRINImage(0xf093), "regular"}, {nil, nil}} {
						s := fixture.Select(`SELECT eo_old.eo_classify($1::bytea) AS kind`, []string{"kind"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.page)
						fixture.BRINBoth(t, p, destination, conn, fixture.BRINMetadata(s, b.PG, false))
					}
					// Strict typed NULL keeps TEXT metadata even without page access.
					s := fixture.Select(`SELECT eo_old.eo_classify(NULL::bytea) AS kind`, []string{"kind"}, []string{"TEXT"}, [][]any{{nil}})
					r(fixture.BRINMetadata(s, b.PG, false))
					r(fixture.Prepared(fixture.BRINMetadata(s, b.PG, false)))
				}
				values(c, b.Recorder)
				run(fixture.Exec(`CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`, 0))
				run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD FUNCTION eo_old.eo_classify(bytea)`, 0))
				values(c, b.Recorder)
				// An e edge forbids standalone DROP, even CASCADE. extension.c
				// supplies only primary/hint; all other public diagnostic fields empty.
				for _, suffix := range []string{"", " CASCADE"} {
					s := fixture.OwnershipError(`DROP FUNCTION eo_old.eo_classify(bytea)`+suffix, "2BP01", `cannot drop function eo_old.eo_classify(bytea) because extension uuid-ossp requires it`)
					s.Diagnostic.Hint = "You can drop extension uuid-ossp instead."
					run(s)
				}
				fixture.OwnershipUnchanged(t, p, c, declarations, baseline)
				// The consumer exists BEFORE origin retirement and must survive it.
				run(fixture.Exec(`CREATE VIEW eo_old.eo_classification AS SELECT eo_old.eo_classify('\x`+hex.EncodeToString(fixture.BRINMeta())+`'::bytea) AS kind`, 0))
				run(fixture.Exec(`DROP EXTENSION pageinspect`, 0))
				if !foreign {
					run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP FUNCTION eo_old.eo_classify(bytea)`, 0))
				}
				fixture.OwnershipUnchanged(t, p, c, identity, before)
				check := func(conn *sql.Conn, recorder *fixture.Recorder) {
					destination := b
					destination.Recorder = recorder
					r := fixture.OwnershipRun(t, p, destination, conn)
					values(conn, recorder)
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_extension WHERE extname='pageinspect'`, 0))
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_proc WHERE pronamespace='eo_old'::regnamespace AND proname IN ('brin_page_type','brin_metapage_info','brin_revmap_data','brin_page_items')`, 0))
					member := ""
					if foreign {
						member = "uuid-ossp"
					}
					r(fixture.Select(`SELECT COALESCE(e.extname::text,'') AS member_of FROM pg_proc p LEFT JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid WHERE p.oid='eo_old.eo_classify(bytea)'::regprocedure`, []string{"member_of"}, []string{"TEXT"}, [][]any{{member}}))
					r(fixture.Select(`SELECT r.rolname,p.proargtypes::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.prosrc,has_function_privilege('eo_pi_acl',p.oid,'EXECUTE') AS allowed FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='eo_old.eo_classify(bytea)'::regprocedure`, []string{"rolname", "proargtypes", "prorettype", "proisstrict", "provolatile", "proretset", "proparallel", "prosrc", "allowed"}, []string{"NAME", "TEXT", "INT8", "BOOL", "CHAR", "BOOL", "CHAR", "TEXT", "BOOL"}, [][]any{{"eo_pi_owner", "17", int64(25), true, "v", false, "s", "brin_page_type", true}}))
					s := fixture.BRINMetadata(fixture.Select(`SELECT kind FROM eo_old.eo_classification`, []string{"kind"}, []string{"TEXT"}, [][]any{{"meta"}}), b.PG, true)
					r(s)
					r(fixture.Prepared(s))
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='eo_old.eo_classify(bytea)'::regprocedure AND refclassid='pg_namespace'::regclass AND refobjid='eo_old'::regnamespace AND deptype='n'`, 1))
					// A view's rewrite rule is a genuine normal dependency on the
					// detached function, not a dependency on its historical provider.
					if !foreign {
						s := fixture.OwnershipError(`DROP FUNCTION eo_old.eo_classify(bytea)`, "2BP01", `cannot drop function eo_old.eo_classify(bytea) because other objects depend on it`)
						s.Diagnostic.Detail = `view eo_old.eo_classification depends on function eo_old.eo_classify(bytea)`
						s.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
						r(s)
					} else {
						s := fixture.OwnershipError(`DROP FUNCTION eo_old.eo_classify(bytea) CASCADE`, "2BP01", `cannot drop function eo_old.eo_classify(bytea) because extension uuid-ossp requires it`)
						s.Diagnostic.Hint = "You can drop extension uuid-ossp instead."
						r(s)
					}
				}
				check(c, b.Recorder)
				// Symbolic declarations and ACL text compare across remapped OIDs;
				// numeric OIDs are asserted stable only within the original engine.
				logical := `SELECT p.proname,n.nspname,r.rolname,p.proacl::text,p.proargtypes::text,p.proallargtypes::text,p.proargmodes::text,p.proargnames::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='eo_old.eo_classify(bytea)'::regprocedure`
				current := fixture.OwnershipSnapshot(t, p, c, logical)
				if !b.PG {
					saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
					connector, err := stuntgres.NewDriver().OpenConnector("eo_pageinspect_" + name)
					if err != nil {
						t.Fatal(err)
					}
					recorded, recorder := fixture.RecordedNativePool(t, connector)
					other := sql.OpenDB(connector)
					defer other.Close()
					for _, q := range []string{`CREATE ROLE eo_pi_owner SUPERUSER`, `CREATE ROLE eo_pi_acl`} {
						if _, err := other.ExecContext(p.Ctx, q); err != nil {
							t.Fatal(err)
						}
					}
					for _, destination := range []struct {
						control  *sql.DB
						recorded *sql.DB
						recorder *fixture.Recorder
					}{
						{p.Native, b.DB, b.Recorder},
						{other, recorded, recorder},
					} {
						if err := stuntgres.Reset(p.Ctx, destination.control); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, destination.control, strings.NewReader(saved)); err != nil {
							t.Fatal(err)
						}
						conn, err := destination.recorded.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						check(conn, destination.recorder)
						fixture.OwnershipUnchanged(t, p, conn, logical, current)
						backend := b
						backend.Recorder = destination.recorder
						ownershipPageinspectCleanup(fixture.OwnershipRun(t, p, backend, conn), foreign)
						conn.Close()
					}
				} else {
					ownershipPageinspectCleanup(run, foreign)
				}
			})
		})
	}
}

func ownershipPageinspectCleanup(run func(fixture.Step), foreign bool) {
	// Remove the real normal consumer first, then the current e edge, then the
	// function. Never CASCADE away a survivor to hide a missing dependency.
	run(fixture.Exec(`DROP VIEW eo_old.eo_classification`, 0))
	if foreign {
		run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP FUNCTION eo_old.eo_classify(bytea)`, 0))
	}
	run(fixture.Exec(`DROP FUNCTION eo_old.eo_classify(bytea)`, 0))
	run(fixture.Exec(`DROP EXTENSION "uuid-ossp"`, 0))
}
