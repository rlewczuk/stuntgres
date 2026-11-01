package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func regLookupStoredSelect(b fixture.Backend, query string, names, types []string, values [][]any) fixture.Step {
	s := regObjectSelect(query, names, types, values)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, column := range columns {
			if types[i] != "REGCOLLATION" && types[i] != "REGROLE" {
				continue
			}
			scan := reflect.TypeFor[string]()
			if b.PG {
				scan = reflect.TypeFor[any]()
			}
			if column.ScanType() != scan {
				t.Errorf("%s stored ScanType=%v want %v", names[i], column.ScanType(), scan)
			}
			n, nk := column.Nullable()
			l, lk := column.Length()
			precision, scale, dk := column.DecimalSize()
			if n != !b.PG || nk != !b.PG || l != 0 || lk || precision != 0 || scale != 0 || dk {
				t.Errorf("%s stored flags nullable=%t/%t length=%d/%t decimal=%d,%d/%t", names[i], n, nk, l, lk, precision, scale, dk)
			}
		}
	}
	return s
}

func regLookupCreateStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	regObjectExec(t, p, b, c,
		"CREATE SCHEMA cl_store",
		`CREATE COLLATION cl_store.named FROM pg_catalog."C"`,
		`CREATE TABLE public.cl_store_data(id integer,c regcollation DEFAULT to_regcollation('cl_store.named'),r regrole DEFAULT to_regrole('pg_monitor'))`,
		`CREATE VIEW public.cl_store_view AS SELECT to_regcollation('cl_store.named') AS c,to_regrole('pg_monitor') AS r,to_regcollation(NULL::text) AS cn,to_regrole(NULL::text) AS rn,to_regcollation('cl_store_absent') AS cm,to_regrole('cl_store_absent') AS rm`)
	regObjectRunStep(t, p, b, c, fixture.Exec("INSERT INTO public.cl_store_data(id) VALUES(1)", 1))
}

func regLookupStoredChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	regObjectBoth(t, p, b, c, regLookupStoredSelect(b, "SELECT c,r,cn,rn,cm,rm FROM public.cl_store_view", []string{"c", "r", "cn", "rn", "cm", "rm"}, []string{"REGCOLLATION", "REGROLE", "REGCOLLATION", "REGROLE", "REGCOLLATION", "REGROLE"}, [][]any{{"cl_store.named", "pg_monitor", nil, nil, nil, nil}}))
	regObjectBoth(t, p, b, c, regLookupStoredSelect(b, "SELECT c,r FROM public.cl_store_data ORDER BY id", []string{"c", "r"}, []string{"REGCOLLATION", "REGROLE"}, [][]any{{"cl_store.named", "pg_monitor"}}))
	regObjectBoth(t, p, b, c, fixture.Select(`SELECT c::oid='cl_store.named'::regcollation::oid AS c,r::oid::bigint AS r FROM public.cl_store_data WHERE id=1`, []string{"c", "r"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(3373)}}))
	regObjectRunStep(t, p, b, c, fixture.Exec("INSERT INTO public.cl_store_data(id) VALUES(2)", 1))
	regObjectBoth(t, p, b, c, regLookupStoredSelect(b, "SELECT c,r FROM public.cl_store_data WHERE id=2", []string{"c", "r"}, []string{"REGCOLLATION", "REGROLE"}, [][]any{{"cl_store.named", "pg_monitor"}}))
	regObjectBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM public.cl_store_data", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
	regObjectRunStep(t, p, b, c, fixture.Exec("DELETE FROM public.cl_store_data WHERE id=2", 1))
}

func regLookupStoredControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, nativeDump *[]byte) {
	t.Helper()
	if !b.PG {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if dump.Len() == 0 || strings.Contains(dump.String(), "CREATE ROLE") {
			t.Fatal("invalid actual lookup dump", dump.String())
		}
		*nativeDump = bytes.Clone(dump.Bytes())
		held, err := c.QueryContext(p.Ctx, "SELECT c,r FROM public.cl_store_data ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		s := regLookupStoredSelect(b, "", []string{"c", "r"}, []string{"REGCOLLATION", "REGROLE"}, nil)
		s.CheckMetadata(t, held)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(*nativeDump)); err != nil {
			t.Fatal(err)
		}
		fixture.ArrayReferenceConsumeHeld(t, held, [][]any{{"cl_store.named", "pg_monitor"}}, []string{"REGCOLLATION", "REGROLE"})
		regLookupStoredChecks(t, p, b, c)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(*nativeDump, redump.Bytes()) {
			t.Fatalf("unchanged lookup redump differs\n--- source ---\n%s\n--- restored ---\n%s", *nativeDump, redump.Bytes())
		}
	} else {
		// Replay the real native control stream, not a PostgreSQL Restore API
		// or a separately manufactured script.
		if len(*nativeDump) == 0 {
			t.Fatal("missing actual native lookup dump for PostgreSQL replay")
		}
		regObjectExec(t, p, b, c, "DROP VIEW public.cl_store_view", "DROP TABLE public.cl_store_data", "DROP SCHEMA cl_store CASCADE")
		if _, err := c.ExecContext(p.Ctx, string(*nativeDump)); err != nil {
			t.Fatal("PostgreSQL rejected actual native lookup dump", err)
		}
		regObjectExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		regLookupStoredChecks(t, p, b, c)
	}
}

func TestReferenceRegLookupCollRoleStored(t *testing.T) {
	var nativeDump []byte
	regLookupScenario(t, []string{"cl_store"}, []string{"cl_stored_role", "cl_stored_renamed"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regLookupCreateStored(t, p, b, c)
		regLookupStoredChecks(t, p, b, c)
		regObjectExec(t, p, b, c, "CREATE ROLE cl_stored_role", `CREATE VIEW public.cl_dynamic_role AS SELECT to_regrole('cl_stored_role') AS r,to_regrole('cl_stored_role')::oid::bigint AS id`)
		id := regLookupIdentity(t, p, c, `SELECT oid::bigint FROM pg_catalog.pg_roles WHERE rolname='cl_stored_role'`)
		regObjectBoth(t, p, b, c, regLookupStoredSelect(b, "SELECT r,id FROM public.cl_dynamic_role", []string{"r", "id"}, []string{"REGROLE", "INT8"}, [][]any{{"cl_stored_role", id}}))
		regObjectExec(t, p, b, c, "ALTER ROLE cl_stored_role RENAME TO cl_stored_renamed")
		regObjectBoth(t, p, b, c, regLookupStoredSelect(b, "SELECT r,id FROM public.cl_dynamic_role", []string{"r", "id"}, []string{"REGROLE", "INT8"}, [][]any{{nil, nil}}))
		regObjectExec(t, p, b, c, "DROP ROLE cl_stored_renamed")
		regObjectBoth(t, p, b, c, regLookupStoredSelect(b, "SELECT r,id FROM public.cl_dynamic_role", []string{"r", "id"}, []string{"REGROLE", "INT8"}, [][]any{{nil, nil}}))
		regObjectExec(t, p, b, c, "CREATE ROLE cl_stored_role")
		newID := regLookupIdentity(t, p, c, `SELECT oid::bigint FROM pg_catalog.pg_roles WHERE rolname='cl_stored_role'`)
		if newID == id {
			t.Fatal("stored runtime lookup reused dropped role identity")
		}
		regObjectBoth(t, p, b, c, regLookupStoredSelect(b, "SELECT r,id FROM public.cl_dynamic_role", []string{"r", "id"}, []string{"REGROLE", "INT8"}, [][]any{{"cl_stored_role", newID}}))
		regObjectExec(t, p, b, c, "DROP VIEW public.cl_dynamic_role", "DROP ROLE cl_stored_role")
		regLookupStoredControls(t, p, b, c, &nativeDump)
	})
}

func TestReferenceRegLookupCollRoleSmoke(t *testing.T) {
	var nativeDump []byte
	regLookupScenario(t, []string{"cl_store"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectBoth(t, p, b, c, regLookupSelect(b, `SELECT to_regcollation('pg_catalog."C"') AS c,to_regrole('pg_monitor') AS r,to_regcollation('cr_absent') AS cm,to_regrole('cr_absent') AS rm,to_regcollation(NULL::text) AS cn,to_regrole(NULL::text) AS rn`, []string{"c", "r", "cm", "rm", "cn", "rn"}, []string{"REGCOLLATION", "REGROLE", "REGCOLLATION", "REGROLE", "REGCOLLATION", "REGROLE"}, [][]any{{`"C"`, "pg_monitor", nil, nil, nil, nil}}))
		regObjectBoth(t, p, b, c, fixture.Select(`SELECT to_regcollation('pg_catalog."C"')::oid::bigint AS c,to_regrole('pg_monitor')::oid::bigint AS r`, []string{"c", "r"}, []string{"INT8", "INT8"}, [][]any{{int64(950), int64(3373)}}))
		q := "SELECT to_regcollation($1::text) AS c,to_regrole($2::text) AS r"
		held := regLookupHeld(t, p, c, q)
		defer held.Statement.Close()
		for _, tc := range []struct{ c, r, cv, rv any }{
			{`pg_catalog."C"`, "pg_monitor", `"C"`, "pg_monitor"},
			{"cr_absent", nil, nil, nil},
			{`pg_catalog."C"`, "pg_monitor", `"C"`, "pg_monitor"},
		} {
			regObjectRunStep(t, p, b, held, regLookupSelect(b, q, []string{"c", "r"}, []string{"REGCOLLATION", "REGROLE"}, [][]any{{tc.cv, tc.rv}}, tc.c, tc.r))
		}
		regObjectBoth(t, p, b, c, regLookupSelect(b, `SELECT to_regcollation('s..t') AS c,to_regrole('s..t') AS r`, []string{"c", "r"}, []string{"REGCOLLATION", "REGROLE"}, [][]any{{nil, nil}}))
		regObjectBoth(t, p, b, c, regLookupError(`SELECT to_regcollation('x.y.z.w')`, "42601", "improper qualified name (too many dotted names): x.y.z.w"))
		regLookupCreateStored(t, p, b, c)
		regLookupStoredChecks(t, p, b, c)
		regLookupStoredControls(t, p, b, c, &nativeDump)
	})
}
