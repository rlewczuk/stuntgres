//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSchemaControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sc_schema_owner", "sc_table_owner"})
	p := fixture.NewPair(t)
	fixture.SchemaReferenceCleanup(t, "sc_empty", "sc1", "sc2")
	for _, q := range []string{"CREATE USER sc_schema_owner", "CREATE USER sc_table_owner"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	oracle, err := fixture.Admin.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer oracle.Close()
	defer oracle.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
	// The oracle setup uses real schema privileges, not a production native grant API.
	for _, q := range []string{"CREATE SCHEMA sc_empty AUTHORIZATION sc_schema_owner", "CREATE SCHEMA sc1 AUTHORIZATION sc_schema_owner", "CREATE SCHEMA sc2 AUTHORIZATION sc_schema_owner", "GRANT CREATE,USAGE ON SCHEMA sc1,sc2 TO sc_table_owner", "SET SESSION AUTHORIZATION sc_table_owner", "CREATE TABLE sc1.t(id INT NOT NULL,note TEXT,flag BOOL,big BIGINT)", "CREATE TABLE sc2.t(id INT NOT NULL,note TEXT,flag BOOL,big BIGINT)", "INSERT INTO sc1.t VALUES(7,NULL,true,9223372036854775807)", "INSERT INTO sc2.t VALUES(9,'kept',false,1)", "RESET SESSION AUTHORIZATION"} {
		if _, err := oracle.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	script := "CREATE SCHEMA sc_empty AUTHORIZATION sc_schema_owner; CREATE SCHEMA sc1 AUTHORIZATION sc_schema_owner; CREATE SCHEMA sc2 AUTHORIZATION sc_schema_owner; SET SESSION AUTHORIZATION sc_table_owner; CREATE TABLE sc1.t(id INT NOT NULL,note TEXT,flag BOOL,big BIGINT); CREATE TABLE sc2.t(id INT NOT NULL,note TEXT,flag BOOL,big BIGINT); INSERT INTO sc1.t VALUES(7,NULL,true,9223372036854775807); INSERT INTO sc2.t VALUES(9,'kept',false,1);"
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	verify := func() {
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				schema string
				row    []any
			}{{"sc1", []any{int64(7), nil, true, int64(9223372036854775807)}}, {"sc2", []any{int64(9), "kept", false, int64(1)}}} {
				s := fixture.Select("SELECT * FROM "+tc.schema+".t", []string{"id", "note", "flag", "big"}, []string{"INT4", "TEXT", "BOOL", "INT8"}, [][]any{tc.row})
				s.Prepared = prepared
				p.Step(t, s)
			}
		}
		for _, name := range []string{"sc1", "sc2", "sc_empty"} {
			var owner string
			if err := oracle.QueryRowContext(p.Ctx, "SELECT r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname=$1", name).Scan(&owner); err != nil || owner != "sc_schema_owner" {
				t.Fatal("oracle namespace owner", name, owner, err)
			}
		}
		for _, name := range []string{"sc1", "sc2"} {
			var owner string
			if err := oracle.QueryRowContext(p.Ctx, "SELECT r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname=$1 AND c.relname='t'", name).Scan(&owner); err != nil || owner != "sc_table_owner" {
				t.Fatal("oracle table owner", name, owner, err)
			}
			rows, err := oracle.QueryContext(p.Ctx, "SELECT a.attname,a.attnotnull FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='t' AND a.attnum>0 ORDER BY a.attnum", name)
			if err != nil {
				t.Fatal(err)
			}
			expected := []string{"id", "note", "flag", "big"}
			i := 0
			for rows.Next() {
				var label string
				var notnull bool
				if err = rows.Scan(&label, &notnull); err != nil || i >= len(expected) || label != expected[i] || notnull != (i == 0) {
					rows.Close()
					t.Fatal("oracle nullability", label, notnull, err)
				}
				i++
			}
			if err = rows.Err(); err != nil || i != 4 {
				rows.Close()
				t.Fatal("oracle attributes", i, err)
			}
			rows.Close()
		}
	}
	verify()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dump.String(), `CREATE SCHEMA "sc1" AUTHORIZATION "sc_schema_owner";`) || !strings.Contains(dump.String(), `CREATE SCHEMA "sc_empty" AUTHORIZATION "sc_schema_owner";`) {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	verify()
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE SCHEMA sc_bad; INSERT INTO absent VALUES(1);")); err == nil {
		t.Fatal("invalid restore succeeded")
	}
	verify()
	// Reapply the actual Dump to PostgreSQL with independently provisioned owner roles.
	for _, q := range []string{"DROP SCHEMA sc1 CASCADE", "DROP SCHEMA sc2 CASCADE", "DROP SCHEMA sc_empty"} {
		if _, err := oracle.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// Schema/table owners differ: privileged setup supplies only oracle CREATE authority.
	// Dump deliberately does not serialize ACLs. Execute schema prefix as superuser,
	// then grant oracle destination authority before owner-group CREATE statements.
	lines := strings.Split(dump.String(), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "SET SESSION AUTHORIZATION") {
			if _, err := oracle.ExecContext(p.Ctx, "GRANT CREATE,USAGE ON SCHEMA sc1,sc2 TO sc_table_owner"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := oracle.ExecContext(p.Ctx, line); err != nil {
			t.Fatal(line, err)
		}
	}
	if _, err := oracle.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
		t.Fatal(err)
	}
	verify()
	var count int
	if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_namespace WHERE nspname='sc_empty'").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	p.Step(t, fixture.RoleReferenceError("DROP ROLE sc_schema_owner", "2BP01", `role "sc_schema_owner" cannot be dropped because some objects depend on it`, "owner of schema sc1\nowner of schema sc2\nowner of schema sc_empty", false, "execute"))
}
