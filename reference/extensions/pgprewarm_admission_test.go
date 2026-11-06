package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const pwAdmissionCall = "SELECT pw_part.pg_prewarm($1::oid::regclass,$2::text,$3::text,$4::bigint,$5::bigint) AS warmed"
const pwAdmissionDefault = "SELECT pw_part.pg_prewarm($1::oid::regclass) AS warmed"

func pwAdmissionBounds(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, oid, n int64) {
	t.Helper()
	upper := fmt.Sprint(n - 1)
	for _, tc := range []struct{ first, last any; message string }{
		{n, nil, "starting block number must be between 0 and " + upper},
		{int64(0), n, "ending block number must be between 0 and " + upper},
		{int64(-1), int64(-1), "starting block number must be between 0 and " + upper},
		{int64(0), int64(-1), "ending block number must be between 0 and " + upper},
	} {
		pwBodyError(t, p, b, c, pwError(pwAdmissionCall, "22023", tc.message, oid, "buffer", "main", tc.first, tc.last))
	}
	pwBodyError(t, p, b, c, pwError(pwAdmissionCall, "22023", "fork \"init\" does not exist for this relation", oid, "buffer", "init", int64(-1), int64(-1)))
}

func TestReferencePgPrewarmStorageAdmission(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pw_admission_reader"})
	pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pwCacheInstall(t, p, b, c)
		bcDDL(t, p, b, c, "CREATE ROLE pw_admission_reader", "GRANT USAGE ON SCHEMA pw_part TO pw_admission_reader")
		defer c.ExecContext(p.Ctx, "RESET ROLE")

		t.Run("catalog_producer", func(t *testing.T) {
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			bcBoth(t, p, b, c, fixture.Select("SELECT oid::bigint,relkind::text FROM pg_class WHERE oid='pg_catalog.pg_am'::regclass", []string{"oid", "relkind"}, []string{"INT8", "TEXT"}, [][]any{{int64(2601), "r"}}))
			if !b.PG {
				bcBoth(t, p, b, c, fixture.Select("SELECT amname::text FROM pg_am ORDER BY amname", []string{"amname"}, []string{"TEXT"}, [][]any{{"brin"}, {"btree"}, {"gin"}, {"gist"}, {"hash"}, {"heap"}, {"spgist"}}))
				bcBoth(t, p, b, c, fixture.Select("SELECT relfilenode::bigint,reltoastrelid::bigint FROM pg_class WHERE oid=2601", []string{"relfilenode", "reltoastrelid"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(0)}}))
			}
			// Seven independently specified 124-byte native producer rows occupy
			// 868 bytes, hence one compact block. PG uses its physical extent.
			n := pwExtent(t, p, b, c, "pg_catalog.pg_am", "main", 1)
			if n < 1 { t.Fatal("catalog has no genuine main extent", n) }
			before := bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode IS NOT NULL ORDER BY bufferid", true)
			pwRoutes(t, p, b, c, pwCount(pwAdmissionDefault, n, int64(2601)))
			for _, mode := range []string{"buffer", "read", "prefetch"} {
				pwRoutes(t, p, b, c, pwCount(pwAdmissionCall, 1, int64(2601), mode, "main", int64(0), int64(0)))
			}
			pwAdmissionBounds(t, p, b, c, 2601, n)
			if b.PG {
				pwResident(t, p, b, c, "pg_catalog.pg_am", 0, 0, 0)
			} else if after := bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode IS NOT NULL ORDER BY bufferid", true); !reflect.DeepEqual(before, after) {
				t.Fatal("cache-ineligible catalog changed shared occupancy", before, after)
			}

			// All system ACL changes are transaction-contained. Savepoints recover
			// each expected PostgreSQL error; rollback restores the exact prior ACL.
			var prior sql.NullString
			if err := c.QueryRowContext(p.Ctx, "SELECT relacl::text FROM pg_class WHERE oid=2601").Scan(&prior); err != nil { t.Fatal(err) }
			func() {
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					for _, q := range []string{"RESET ROLE", "ROLLBACK"} {
						if _, err := c.ExecContext(ctx, q); err != nil { t.Error(q, err) }
					}
				}()
				st, err := c.PrepareContext(p.Ctx, pwAdmissionCall)
				if err != nil { t.Fatal(err) }
				defer st.Close()
				held := fixture.HeldStatement{Executor: c, Statement: st, SQL: pwAdmissionCall}
				bcDDL(t, p, b, c, "BEGIN", "REVOKE SELECT ON pg_catalog.pg_am FROM PUBLIC", "SET ROLE pw_admission_reader")
				probe := func(step fixture.Step) {
					step.ErrorStage = "execute"
					for _, route := range []string{"direct", "fresh", "held"} {
						bcDDL(t, p, b, c, "SAVEPOINT pw_catalog_probe")
						switch route {
						case "direct": bcRun(t, p, b, c, step)
						case "fresh": bcRun(t, p, b, c, fixture.Prepared(step))
						case "held": fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
						}
						bcDDL(t, p, b, c, "ROLLBACK TO pw_catalog_probe", "RELEASE pw_catalog_probe")
					}
				}
				denied := pwError(pwAdmissionCall, "42501", "permission denied for table pg_am", int64(2601), "buffer", "init", int64(-1), int64(-1))
				probe(denied)
				bcDDL(t, p, b, c, "RESET ROLE", "GRANT SELECT ON pg_catalog.pg_am TO pw_admission_reader", "SET ROLE pw_admission_reader")
				probe(pwCount(pwAdmissionCall, 1, int64(2601), "buffer", "main", int64(0), int64(0)))
				bcDDL(t, p, b, c, "RESET ROLE", "REVOKE SELECT ON pg_catalog.pg_am FROM pw_admission_reader", "SET ROLE pw_admission_reader")
				probe(denied)
				bcDDL(t, p, b, c, "RESET ROLE", "GRANT SELECT ON pg_catalog.pg_am TO pw_admission_reader", "SET ROLE pw_admission_reader")
				probe(pwCount(pwAdmissionCall, 1, int64(2601), "buffer", "main", int64(0), int64(0)))
			}()
			var restored sql.NullString
			if err := c.QueryRowContext(p.Ctx, "SELECT relacl::text FROM pg_class WHERE oid=2601").Scan(&restored); err != nil || restored != prior { t.Fatal("system ACL rollback did not restore exact predecessor", prior, restored, err) }
			bcDDL(t, p, b, c, "SET ROLE pw_admission_reader")
			pwBodyError(t, p, b, c, pwError(pwAdmissionCall, "42501", "permission denied for table pg_authid", int64(1260), "buffer", "init", int64(-1), int64(-1)))
			bcDDL(t, p, b, c, "RESET ROLE")
		})

		t.Run("inline_large_text", func(t *testing.T) {
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			bcDDL(t, p, b, c, "CREATE TABLE pw_part.inline_text(payload text)", "ALTER TABLE pw_part.inline_text SET(autovacuum_enabled=false)")
			payload := strings.Repeat("inline", 4000)
			bcRun(t, p, b, c, fixture.Exec("INSERT INTO pw_part.inline_text SELECT $1::text FROM generate_series(1,120)", 120, payload))
			bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS matching FROM pw_part.inline_text WHERE payload=$1::text", []string{"matching"}, []string{"INT8"}, [][]any{{int64(120)}}, payload))
			oid := fsOID(t, p, c, "pw_part.inline_text")
			// Native text slots are 68 bytes including line pointer: 120 rows
			// fit one block, while 121 require two, regardless of text length.
			n := pwExtent(t, p, b, c, "pw_part.inline_text", "main", 1)
			pwRoutes(t, p, b, c, pwCount(pwAdmissionDefault, n, oid))
			bcRun(t, p, b, c, fixture.Exec("INSERT INTO pw_part.inline_text VALUES($1::text)", 1, payload))
			bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS matching FROM pw_part.inline_text WHERE payload=$1::text", []string{"matching"}, []string{"INT8"}, [][]any{{int64(121)}}, payload))
			n = pwExtent(t, p, b, c, "pw_part.inline_text", "main", 2)
			node, _, _ := bcStorage(t, p, c, "pw_part.inline_text")
			other := bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode IS NOT NULL AND relfilenode<>$1::oid ORDER BY bufferid", true, node)
			pwRoutes(t, p, b, c, pwCount(pwAdmissionDefault, n, oid))
			for _, mode := range []string{"buffer", "read", "prefetch"} { pwRoutes(t, p, b, c, pwCount(pwAdmissionCall, 1, oid, mode, "main", int64(0), int64(0))) }
			before := pwCoordinates(t, p, b, c, "pw_part.inline_text")
			pwAdmissionBounds(t, p, b, c, oid, n)
			if !b.PG {
				if len(before) != 2 || before[[2]int64{0,0}] == nil || before[[2]int64{0,1}] == nil { t.Fatal("inline text did not populate actual two-block geometry", before) }
				pwSameCoordinates(t, before, pwCoordinates(t, p, b, c, "pw_part.inline_text"))
				if after := bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode IS NOT NULL AND relfilenode<>$1::oid ORDER BY bufferid", true, node); !reflect.DeepEqual(other, after) { t.Fatal("inline preload changed another target", other, after) }
				bcBoth(t, p, b, c, fixture.Select("SELECT reltoastrelid::bigint FROM pg_class WHERE oid=$1::oid", []string{"reltoastrelid"}, []string{"INT8"}, [][]any{{int64(0)}}, oid))
			}
			bcDDL(t, p, b, c, "SET ROLE pw_admission_reader")
			pwBodyError(t, p, b, c, pwError(pwAdmissionCall, "42501", "permission denied for table inline_text", oid, "buffer", "init", int64(-1), int64(-1)))
			bcDDL(t, p, b, c, "RESET ROLE", "GRANT SELECT ON pw_part.inline_text TO pw_admission_reader", "SET ROLE pw_admission_reader")
			pwRoutes(t, p, b, c, pwCount(pwAdmissionDefault, n, oid))
			bcDDL(t, p, b, c, "RESET ROLE")
		})

		if b.PG {
			t.Run("physical_toast_target", func(t *testing.T) {
				defer c.ExecContext(p.Ctx, "RESET ROLE")
				bcDDL(t, p, b, c, "CREATE TABLE pw_part.toast_heap(payload text)", "ALTER TABLE pw_part.toast_heap ALTER COLUMN payload SET STORAGE EXTERNAL", "ALTER TABLE pw_part.toast_heap SET(autovacuum_enabled=false)")
				var parent, toast int64
				var name, namespace, kind string
				var sameOwner bool
				q := "SELECT c.oid::bigint,t.oid::bigint,t.relname::text,n.nspname::text,t.relkind::text,c.relowner=t.relowner FROM pg_class c JOIN pg_class t ON t.oid=c.reltoastrelid JOIN pg_namespace n ON n.oid=t.relnamespace WHERE c.oid='pw_part.toast_heap'::regclass"
				if err := c.QueryRowContext(p.Ctx, q).Scan(&parent, &toast, &name, &namespace, &kind, &sameOwner); err != nil { t.Fatal(err) }
				if parent <= 0 || toast <= 0 || parent == toast || name != fmt.Sprintf("pg_toast_%d", parent) || namespace != "pg_toast" || kind != "t" || !sameOwner { t.Fatal("not the actual parent-owned physical TOAST relation", parent, toast, name, namespace, kind, sameOwner) }
				pwRoutes(t, p, b, c, pwCount(pwAdmissionDefault, 0, toast))
				pwBodyError(t, p, b, c, pwError(pwAdmissionCall, "22023", "starting block number must be between 0 and -1", toast, "buffer", "main", int64(0), nil))
				payload := strings.Repeat("external", 2500)
				bcRun(t, p, b, c, fixture.Exec("INSERT INTO pw_part.toast_heap VALUES($1::text)", 1, payload))
				bcBoth(t, p, b, c, fixture.Select("SELECT payload=$1::text AS same FROM pw_part.toast_heap", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, payload))
				target := namespace + "." + name
				n := pwExtent(t, p, b, c, target, "main", 0)
				if n < 1 { t.Fatal("external insert did not create physical TOAST blocks", n) }
				pwRoutes(t, p, b, c, pwCount(pwAdmissionDefault, n, toast))
				for _, mode := range []string{"buffer", "read", "prefetch"} { pwRoutes(t, p, b, c, pwCount(pwAdmissionCall, 1, toast, mode, "main", int64(0), int64(0))) }
				pwRoutes(t, p, b, c, pwCount(pwAdmissionCall, 1, toast, "buffer", "main", n-1, n-1))
				pwAdmissionBounds(t, p, b, c, toast, n)
				pwResident(t, p, b, c, target, 0, 0, n-1)
				bcDDL(t, p, b, c, "GRANT SELECT ON pw_part.toast_heap TO pw_admission_reader", "SET ROLE pw_admission_reader")
				pwBodyError(t, p, b, c, pwError(pwAdmissionCall, "42501", "permission denied for table "+name, toast, "buffer", "init", int64(-1), int64(-1)))
				bcDDL(t, p, b, c, "RESET ROLE")
			})
		}
	})
}
