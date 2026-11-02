package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceGINPresentationChanges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"gin_presentation_owner"})
	fixture.SchemaReferenceCleanup(t, "gin_presentation_schema")
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
		type seed struct {
			kind, relation, prefix string
			oid                    int
			name, schema, owner    string
		}
		seeds := []seed{
			{kind: "CLASS", relation: "pg_opclass", prefix: "opc", oid: 10064},
			{kind: "CLASS", relation: "pg_opclass", prefix: "opc", oid: 10073},
			{kind: "FAMILY", relation: "pg_opfamily", prefix: "opf", oid: 2745},
			{kind: "FAMILY", relation: "pg_opfamily", prefix: "opf", oid: 3659},
		}
		location := func(ctx context.Context, s seed) (schema, name, owner string, err error) {
			q := "SELECT n.nspname,o." + s.prefix + "name,pg_get_userbyid(o." + s.prefix + "owner) FROM " + s.relation + " o JOIN pg_namespace n ON n.oid=o." + s.prefix + "namespace WHERE o.oid=$1::oid"
			err = conn.QueryRowContext(ctx, q, s.oid).Scan(&schema, &name, &owner)
			return
		}
		for i := range seeds {
			var err error
			seeds[i].schema, seeds[i].name, seeds[i].owner, err = location(p.Ctx, seeds[i])
			if err != nil {
				t.Fatal("capture cleanup identity", err)
			}
		}
		// These builtin identities are shared outside public. Resolve each by its
		// fixed OID at cleanup time, even after an intermediate assertion fails.
		// Cleanup owns a fresh bounded context, never an expired scenario context.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP TABLE IF EXISTS public.gin_presentation_rows"} {
				if _, err := conn.ExecContext(ctx, q); err != nil {
					t.Error("presentation cleanup", q, err)
				}
			}
			for _, original := range seeds {
				for _, action := range []string{"owner", "schema", "name"} {
					schema, name, owner, err := location(ctx, original)
					if err != nil {
						t.Error("locate builtin during cleanup", original.oid, err)
						continue
					}
					clause := ""
					switch {
					case action == "owner" && owner != original.owner:
						clause = " OWNER TO " + quote(original.owner)
					case action == "schema" && schema != original.schema:
						clause = " SET SCHEMA " + quote(original.schema)
					case action == "name" && name != original.name:
						clause = " RENAME TO " + quote(original.name)
					}
					if clause != "" {
						q := "ALTER OPERATOR " + original.kind + " " + quote(schema) + "." + quote(name) + " USING gin" + clause
						if _, err := conn.ExecContext(ctx, q); err != nil {
							t.Error("restore builtin presentation", q, err)
						}
					}
				}
				schema, name, owner, err := location(ctx, original)
				if err != nil || schema != original.schema || name != original.name || owner != original.owner {
					t.Error("builtin cleanup did not restore original identity", original.oid, schema, name, owner, err)
				}
			}
		}()
		for _, q := range []string{
			"CREATE ROLE gin_presentation_owner",
			"CREATE SCHEMA gin_presentation_schema",
			"CREATE TABLE gin_presentation_rows(id integer,a integer[],v tsvector)",
			"INSERT INTO gin_presentation_rows VALUES(1,'{1,2,2}','cat:1 rat:2'),(2,'{2}','dog:1')",
			"CREATE INDEX gin_presentation_i ON gin_presentation_rows USING gin(a,v)",
		} {
			count := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				count = 2
			}
			run(fixture.Exec(q, count))
		}
		const selectQuery = "SELECT id FROM gin_presentation_rows WHERE a @> $1::integer[] AND v @@ $2::tsquery ORDER BY id"
		const insertQuery = "INSERT INTO gin_presentation_rows VALUES($1,$2::integer[],$3::tsvector)"
		const updateQuery = "UPDATE gin_presentation_rows SET a=$1::integer[],v=$2::tsvector WHERE id=$3"
		held := classHeld(t, p, conn, selectQuery)
		insert := classHeld(t, p, conn, insertQuery)
		update := classHeld(t, p, conn, updateQuery)
		classNames, familyNames := []string{"array_ops", "tsvector_ops"}, []string{"array_ops", "tsvector_ops"}
		classSchemas, familySchemas := []string{"pg_catalog", "pg_catalog"}, []string{"pg_catalog", "pg_catalog"}
		classOwned, familyOwned := false, false
		metadata := func() {
			// OIDs and class/family links are released facts. Owner equivalence is
			// expressed against the fixed original owner OID, not its client label.
			const classes = "SELECT c.oid::bigint,c.opcname,n.nspname,c.opcmethod::bigint,c.opcfamily::bigint,c.opcintype::bigint,c.opcdefault,c.opckeytype::bigint,(c.opcowner=CASE WHEN $1 THEN (SELECT oid FROM pg_roles WHERE rolname='gin_presentation_owner') ELSE 10::oid END) AS owned FROM pg_opclass c JOIN pg_namespace n ON n.oid=c.opcnamespace WHERE c.oid IN(10064,10073) ORDER BY c.oid"
			classRoutes(t, p, b, conn, fixture.Select(classes,
				[]string{"oid", "opcname", "nspname", "opcmethod", "opcfamily", "opcintype", "opcdefault", "opckeytype", "owned"},
				[]string{"INT8", "NAME", "NAME", "INT8", "INT8", "INT8", "BOOL", "INT8", "BOOL"},
				[][]any{{int64(10064), classNames[0], classSchemas[0], int64(2742), int64(2745), int64(2277), true, int64(2283), true}, {int64(10073), classNames[1], classSchemas[1], int64(2742), int64(3659), int64(3614), true, int64(25), true}}, classOwned))
			const families = "SELECT f.oid::bigint,f.opfname,n.nspname,f.opfmethod::bigint,(f.opfowner=CASE WHEN $1 THEN (SELECT oid FROM pg_roles WHERE rolname='gin_presentation_owner') ELSE 10::oid END) AS owned FROM pg_opfamily f JOIN pg_namespace n ON n.oid=f.opfnamespace WHERE f.oid IN(2745,3659) ORDER BY f.oid"
			classRoutes(t, p, b, conn, fixture.Select(families, []string{"oid", "opfname", "nspname", "opfmethod", "owned"}, []string{"INT8", "NAME", "NAME", "INT8", "BOOL"},
				[][]any{{int64(2745), familyNames[0], familySchemas[0], int64(2742), true}, {int64(3659), familyNames[1], familySchemas[1], int64(2742), true}}, familyOwned))
			classRoutes(t, p, b, conn, fixture.Select("SELECT c.relam::bigint,i.indclass::text,pg_get_indexdef(c.oid,1,true) AS a,pg_get_indexdef(c.oid,2,true) AS v FROM pg_class c JOIN pg_index i ON i.indexrelid=c.oid WHERE c.oid='gin_presentation_i'::regclass",
				[]string{"relam", "indclass", "a", "v"}, []string{"INT8", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(2742), "10064 10073", "a", "v"}}))
		}
		check := func() {
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_presentation_rows ORDER BY id", []int64{1, 2}))
			step := ginIDs(selectQuery, []int64{1}, "{2}", "cat <-> rat")
			classRoutes(t, p, b, conn, step)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			metadata()
		}
		postDML := func() {
			fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertQuery, 1, 3, "{3,3}", "cat:1 rat:2"))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_presentation_rows WHERE v @@ 'cat <-> rat'::tsquery ORDER BY id", []int64{1, 3}))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_presentation_rows WHERE a @> '{3}'::integer[] ORDER BY id", []int64{3}))
			fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.Exec(updateQuery, 1, "{4}", "dog:1", 3))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_presentation_rows WHERE a @> '{3}'::integer[] ORDER BY id", nil))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_presentation_rows WHERE a @> '{4}'::integer[] AND v @@ 'dog'::tsquery ORDER BY id", []int64{3}))
			run(fixture.Prepared(fixture.Exec("DELETE FROM gin_presentation_rows WHERE id=$1", 1, 3)))
			check()
		}
		check()
		for _, kind := range []string{"CLASS", "FAMILY"} {
			for _, action := range []string{"rename", "owner", "schema"} {
				names, schemas := classNames, classSchemas
				if kind == "FAMILY" {
					names, schemas = familyNames, familySchemas
				}
				for column := range names {
					clause := ""
					switch action {
					case "rename":
						clause = " RENAME TO " + fmt.Sprintf("gin_presentation_%s_%d", strings.ToLower(kind), column)
					case "owner":
						clause = " OWNER TO gin_presentation_owner"
					case "schema":
						clause = " SET SCHEMA gin_presentation_schema"
					}
					run(fixture.Exec("ALTER OPERATOR "+kind+" "+quote(schemas[column])+"."+quote(names[column])+" USING gin"+clause, 0))
					if action == "rename" {
						names[column] = fmt.Sprintf("gin_presentation_%s_%d", strings.ToLower(kind), column)
					}
					if action == "schema" {
						schemas[column] = "gin_presentation_schema"
					}
				}
				if action == "owner" {
					if kind == "CLASS" {
						classOwned = true
					} else {
						familyOwned = true
					}
				}
				postDML()
			}
		}
		// A private class presentation change and its indexed write must roll
		// back together, without poisoning the existing held write/query plans.
		run(fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertQuery, 1, 3, "{3}", "cat:1 rat:2"))
		run(fixture.Exec("ALTER OPERATOR CLASS gin_presentation_schema.gin_presentation_class_0 USING gin RENAME TO gin_presentation_private", 0))
		classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_presentation_rows WHERE v @@ 'cat <-> rat'::tsquery ORDER BY id", []int64{1, 3}))
		classRoutes(t, p, b, conn, ginIDs("SELECT id FROM gin_presentation_rows WHERE a @> '{3}'::integer[] ORDER BY id", []int64{3}))
		run(fixture.Exec("ROLLBACK", 0))
		check()
		postDML()
		if !b.PG { // Native controls have no PostgreSQL API equivalent.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), "ALTER OPERATOR CLASS") || !strings.Contains(dump.String(), "ALTER OPERATOR FAMILY") {
				t.Fatal("presentation changes missing from native replay", dump.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
			postDML()
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatal("presentation replay or subsequent DML changed canonical source state", err)
			}
		}
	})
}
