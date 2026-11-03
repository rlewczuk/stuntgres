package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatioUserIndexesShape(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		statioExec(t, p, b, c, "CREATE TABLE statio_user_shape(k integer)", "CREATE INDEX statio_user_shape_i ON statio_user_shape(k)")
		parent := statioIdentity(t, p, c, "statio_user_shape")
		index := statioIdentity(t, p, c, "statio_user_shape_i")
		var parentValue, indexValue any = parent, index
		if b.PG {
			parentValue, indexValue = strconv.FormatInt(parent, 10), strconv.FormatInt(index, 10)
		}
		for _, view := range []string{"pg_statio_user_indexes", "pg_catalog.pg_statio_user_indexes"} {
			for _, predicate := range []string{"false", "relname='statio_user_shape'"} {
				q := "SELECT * FROM " + view + " WHERE " + predicate + " ORDER BY indexrelname"
				got := statioRows(t, p, b, c, q)
				var want [][]any
				if predicate != "false" {
					want = [][]any{{parentValue, indexValue, "public", "statio_user_shape", "statio_user_shape_i", int64(0), int64(0)}}
					// PostgreSQL may have real physical activity during index creation.
					if b.PG && len(got) == 1 {
						if got[0][5].(int64) < 0 || got[0][6].(int64) < 0 {
							t.Fatal("negative physical counters", got)
						}
						want[0][5], want[0][6] = got[0][5], got[0][6]
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("independent seven-column identities", got, want)
				}
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := stmt.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				if prepared := statioConsumeRows(t, rows, b.PG); !reflect.DeepEqual(prepared, got) {
					t.Fatal("prepared shape", prepared, got)
				}
				stmt.Close()
			}
			step := fixture.Select("SELECT indexrelname FROM "+view+" WHERE relid='statio_user_shape'::regclass ORDER BY indexrelname", []string{"indexrelname"}, []string{"NAME"}, [][]any{{"statio_user_shape_i"}})
			vacuumRun(t, p, b, c, step)
			vacuumRun(t, p, b, c, fixture.Prepared(step))
		}
		var attrs [][]any
		for i, name := range statioNames {
			collation := int64(0)
			if i >= 2 && i <= 4 {
				collation = 950
			}
			attrs = append(attrs, []any{name, int64(i + 1), []int64{26, 26, 19, 19, 19, 20, 20}[i], int64(-1), collation, false, false})
		}
		for _, step := range []fixture.Step{
			fixture.Select("SELECT attname,attnum,atttypid::bigint,atttypmod,attcollation::bigint,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_statio_user_indexes'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attnum", "atttypid", "atttypmod", "attcollation", "attnotnull", "attisdropped"}, []string{"NAME", "INT2", "INT8", "INT4", "INT8", "BOOL", "BOOL"}, attrs),
			fixture.Select("SELECT relkind,pg_get_userbyid(relowner) AS owner,relhasindex FROM pg_class WHERE oid='pg_statio_user_indexes'::regclass", []string{"relkind", "owner", "relhasindex"}, []string{"CHAR", "NAME", "BOOL"}, [][]any{{"v", fixture.PGTableViewsOwner(b), false}}),
			fixture.Select("SELECT t.typname,t.typtype,t.typrelid=c.oid AS linked,t.typowner=c.relowner AS owned,a.typelem=t.oid AS element_link,a.typowner=c.relowner AS array_owned FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_statio_user_indexes'::regclass", []string{"typname", "typtype", "linked", "owned", "element_link", "array_owned"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"pg_statio_user_indexes", "c", true, true, true, true}}),
		} {
			vacuumRun(t, p, b, c, step)
			vacuumRun(t, p, b, c, fixture.Prepared(step))
		}
		wantDefinition := " SELECT relid,\n    indexrelid,\n    schemaname,\n    relname,\n    indexrelname,\n    idx_blks_read,\n    idx_blks_hit\n   FROM pg_statio_all_indexes\n  WHERE ((schemaname <> ALL (ARRAY['pg_catalog'::name, 'information_schema'::name])) AND (schemaname !~ '^pg_toast'::text));"
		for _, q := range []string{"SELECT pg_get_viewdef('pg_statio_user_indexes'::regclass) AS definition", "SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_statio_user_indexes'"} {
			step := fixture.Select(q, []string{"definition"}, []string{"TEXT"}, [][]any{{wantDefinition}})
			vacuumRun(t, p, b, c, step)
			vacuumRun(t, p, b, c, fixture.Prepared(step))
		}
	})
}

func TestReferenceStatioUserIndexesPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"statio_user_reader"})
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		admin, reader, peer := vacuumConn(t, p, b), vacuumConn(t, p, b), vacuumConn(t, p, b)
		statioExec(t, p, b, admin, "CREATE ROLE statio_user_reader", "CREATE SCHEMA statio_user_secret", "REVOKE ALL ON SCHEMA statio_user_secret FROM PUBLIC", "CREATE TABLE statio_user_secret.statio_user_hidden(k integer)", "CREATE INDEX statio_user_hidden_i ON statio_user_secret.statio_user_hidden(k)")
		t.Cleanup(func() {
			copyProgressExec(t, p.Ctx, admin, "GRANT SELECT ON pg_catalog.pg_statio_user_indexes TO PUBLIC")
			copyProgressExec(t, p.Ctx, admin, "DROP SCHEMA statio_user_secret CASCADE")
		})
		q := "SELECT * FROM pg_catalog.pg_statio_user_indexes WHERE relname='statio_user_hidden'"
		before := statioRows(t, p, b, admin, q)
		if len(before) != 1 {
			t.Fatal("hidden real index", before)
		}
		for _, c := range []*sql.Conn{reader, peer} {
			statioExec(t, p, b, c, "SET ROLE statio_user_reader")
			vacuumRun(t, p, b, c, fixture.ImplicitError("SELECT k FROM statio_user_secret.statio_user_hidden", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema statio_user_secret", Position: "15"}))
			for _, view := range []string{"pg_statio_user_indexes", "pg_statio_all_indexes"} {
				if got := statioRows(t, p, b, c, "SELECT * FROM "+view+" WHERE relname='statio_user_hidden'"); !reflect.DeepEqual(got, before) {
					t.Fatal("underlying ACL redacted", got, before)
				}
			}
		}
		stmt, err := reader.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		statioExec(t, p, b, admin, "REVOKE SELECT ON pg_catalog.pg_statio_user_indexes FROM PUBLIC")
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for view pg_statio_user_indexes"}
		rows, err := stmt.QueryContext(p.Ctx)
		if rows != nil {
			rows.Close()
			t.Fatal("denied prepared returned rows")
		}
		fixture.AssertDiagnostic(t, err, &want)
		for _, c := range []*sql.Conn{reader, peer} {
			vacuumRun(t, p, b, c, fixture.ImplicitError(q, want))
		}
		statioExec(t, p, b, admin, "GRANT SELECT ON pg_catalog.pg_statio_user_indexes TO PUBLIC")
		rows, err = stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := statioConsumeRows(t, rows, b.PG); !reflect.DeepEqual(got, before) {
			t.Fatal("restored retained execution", got, before)
		}
		statioExec(t, p, b, reader, "RESET ROLE")
		statioExec(t, p, b, peer, "RESET ROLE")
	})
}
