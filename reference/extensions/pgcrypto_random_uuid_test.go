package reference_test

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func pgcryptoRandomUUIDShape(t *testing.T, raw any, pg bool) string {
	t.Helper()
	var value string
	if pg {
		b, ok := raw.([]byte)
		if !ok {
			t.Fatalf("PG UUID raw %T", raw)
		}
		value = string(b)
	} else {
		s, ok := raw.(string)
		if !ok {
			t.Fatalf("native UUID raw %T", raw)
		}
		value = s
	}
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || strings.ToLower(value) != value {
		t.Fatal("canonical UUID", value)
	}
	b, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil || len(b) != 16 || b[6]>>4 != 4 || b[8]&0xc0 != 0x80 {
		t.Fatal("UUID version/variant", value, err)
	}
	return value
}

func pgcryptoRandomUUIDRows(t *testing.T, rows *sql.Rows, pg bool) string {
	t.Helper()
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "UUID" {
		t.Fatal("UUID metadata", meta, err)
	}
	if !rows.Next() {
		t.Fatal("missing UUID", rows.Err())
	}
	var raw any
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	value := pgcryptoRandomUUIDShape(t, raw, pg)
	if rows.Next() || rows.Err() != nil {
		t.Fatal("UUID tail", rows.Err())
	}
	return value
}

func pgcryptoRandomUUIDQuery(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, pg bool) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	pgcryptoRandomUUIDRows(t, rows, pg)
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 3 {
		rows, err = stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		pgcryptoRandomUUIDRows(t, rows, pg)
	}
}

func pgcryptoRandomUUIDBindings(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	var installed int64
	if err := c.QueryRowContext(p.Ctx, "SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='pgcrypto_part' AND proname='gen_random_uuid'").Scan(&installed); err != nil || installed <= 0 {
		t.Fatal(installed, err)
	}
	for i, v := range []struct {
		path, call string
		member     bool
	}{
		{"pgcrypto_part,public", "gen_random_uuid()", false},
		{"pgcrypto_part,pg_catalog", "gen_random_uuid()", true},
		{"pg_catalog,pgcrypto_part", "gen_random_uuid()", false},
		{"pgcrypto_part,pg_catalog", "pg_catalog.gen_random_uuid()", false},
		{"pg_catalog,pgcrypto_part", "pgcrypto_part.gen_random_uuid()", true},
	} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO "+v.path, 0))
		pgcryptoRandomUUIDQuery(t, p, c, "SELECT "+v.call+" AS value", pg)
		name := fmt.Sprintf("pgcrypto_random_binding_%d", i)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE public."+name+"(id integer,u uuid DEFAULT "+v.call+")", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE VIEW public."+name+"_view AS SELECT "+v.call+" AS value", 0))
		want := int64(0)
		if v.member {
			want = 1
		}
		for _, consumer := range []struct {
			query string
			class int64
		}{
			{"SELECT oid FROM pg_attrdef WHERE adrelid='public." + name + "'::regclass::oid AND adnum=2", 2604},
			{"SELECT oid FROM pg_rewrite WHERE ev_class='public." + name + "_view'::regclass::oid AND rulename='_RETURN'", 2618},
		} {
			var id int64
			if err := c.QueryRowContext(p.Ctx, consumer.query).Scan(&id); err != nil || id <= 0 {
				t.Fatal(id, err)
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=$1 AND objid=$2 AND refclassid=1255 AND refobjid=$3 AND deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{want}}, consumer.class, id, installed))
		}
	}
}

func TestReferencePgcryptoRandomUUID(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		pgcryptoRandomUUIDQuery(t, p, c, "SELECT gen_random_uuid() AS value", pg)
		pgcryptoRandomInstall(t, p, c, r)
		pgcryptoRandomUUIDQuery(t, p, c, "SELECT pgcrypto_part.gen_random_uuid() AS value", pg)
		pgcryptoRandomUUIDBindings(t, p, c, r, pg)
	})
}
