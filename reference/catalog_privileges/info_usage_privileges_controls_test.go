package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const upControlQuery = "SELECT * FROM information_schema.usage_privileges WHERE object_schema='public' AND object_name LIKE 'up_ctl_%'"
const upLinksQuery = `SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='usage_privileges' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`

func upAssertRows(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	defer rows.Close()
	got, expected := map[[8]string]int{}, map[[8]string]int{}
	for _, row := range want {
		var key [8]string
		for i := range key {
			key[i] = row[i].(string)
		}
		expected[key]++
	}
	for rows.Next() {
		var key [8]string
		targets := make([]any, 8)
		for i := range targets {
			targets[i] = &key[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		got[key]++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("origin bag=%v want=%v", got, expected)
	}
}

func (c *upCase) links(e fixture.Executor) {
	c.t.Helper()
	s := fixture.Select(upLinksQuery, []string{"row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}})
	c.run(e, s)
	c.run(e, fixture.Prepared(s))
	var logical [][]any
	for i, name := range upColumns {
		domain := "sql_identifier"
		if i >= 5 {
			domain = "character_data"
		}
		if i == 7 {
			domain = "yes_or_no"
		}
		logical = append(logical, []any{name, "information_schema", domain, false, int64(-1), int64(950)})
	}
	s = fixture.Select("SELECT a.attname,n.nspname,t.typname,a.attnotnull,a.atttypmod,a.attcollation::bigint FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.usage_privileges'::regclass AND a.attnum>0 ORDER BY a.attnum", []string{"attname", "nspname", "typname", "attnotnull", "atttypmod", "attcollation"}, []string{"NAME", "NAME", "NAME", "BOOL", "INT4", "INT8"}, logical)
	c.run(e, s)
	c.run(e, fixture.Prepared(s))
}

func (c *upCase) controlACLs(e fixture.Executor) {
	c.t.Helper()
	for _, tc := range []struct {
		name, query string
		absent      bool
	}{
		{"up_ctl_raw", "SELECT typacl IS NULL AS absent,COALESCE(cardinality(typacl),0)::bigint AS entries FROM pg_type WHERE oid=$1::regtype", true},
		{"up_ctl_empty", "SELECT typacl IS NULL AS absent,COALESCE(cardinality(typacl),0)::bigint AS entries FROM pg_type WHERE oid=$1::regtype", false},
		{"up_ctl_rawseq", "SELECT relacl IS NULL AS absent,COALESCE(cardinality(relacl),0)::bigint AS entries FROM pg_class WHERE oid=$1::regclass", true},
		{"up_ctl_emptyseq", "SELECT relacl IS NULL AS absent,COALESCE(cardinality(relacl),0)::bigint AS entries FROM pg_class WHERE oid=$1::regclass", false},
	} {
		c.run(e, fixture.Select(tc.query, []string{"absent", "entries"}, []string{"BOOL", "INT8"}, [][]any{{tc.absent, int64(0)}}, "public."+tc.name))
	}
}

func TestReferenceInfoUsagePrivilegesControls(t *testing.T) {
	upScenario(t, func(c *upCase) {
		c.seed()
		c.exec("GRANT CREATE ON SCHEMA public TO up_o", "SET ROLE up_o",
			"CREATE DOMAIN public.up_ctl_domain AS integer", "GRANT USAGE ON DOMAIN public.up_ctl_domain TO up_r WITH GRANT OPTION",
			"CREATE SEQUENCE public.up_ctl_sequence", "GRANT USAGE ON SEQUENCE public.up_ctl_sequence TO PUBLIC,up_r",
			"CREATE DOMAIN public.up_ctl_raw AS integer", "CREATE DOMAIN public.up_ctl_empty AS integer", "REVOKE ALL ON DOMAIN public.up_ctl_empty FROM PUBLIC,up_o",
			"CREATE SEQUENCE public.up_ctl_rawseq", "CREATE SEQUENCE public.up_ctl_emptyseq", "REVOKE ALL ON SEQUENCE public.up_ctl_emptyseq FROM up_o", "RESET ROLE",
			"CREATE VIEW public.up_consumer AS "+upControlQuery, "CREATE TABLE public.up_sentinel(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.up_sentinel VALUES(91)", 1))
		want := append(c.baseline("up_o", "public", "up_ctl_domain"), c.row("up_o", "up_r", "public", "up_ctl_domain", "YES"))
		want = append(want, c.baseline("up_o", "public", "up_ctl_raw")...)
		want = append(want, c.familyRow("up_o", "up_o", "public", "up_ctl_sequence", "SEQUENCE", "YES"), c.familyRow("up_o", "PUBLIC", "public", "up_ctl_sequence", "SEQUENCE", "NO"), c.familyRow("up_o", "up_r", "public", "up_ctl_sequence", "SEQUENCE", "NO"))
		held := c.hold(c.conn, upControlQuery)
		check := func(e fixture.Executor, expected [][]any) {
			c.run(e, c.step(upControlQuery, expected))
			c.run(e, fixture.Prepared(c.step(upControlQuery, expected)))
		}
		check(c.conn, want)
		c.run(held, c.step(upControlQuery, want))
		c.run(c.conn, c.step("SELECT * FROM public.up_consumer", want))
		c.links(c.conn)
		c.controlACLs(c.conn)
		// Reset/Dump/Restore are host controls, not PostgreSQL SQL operations.
		if c.b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
			c.t.Fatal(err)
		}
		text := strings.ToLower(strings.ReplaceAll(dump.String(), `"`, ""))
		for _, prefix := range []string{"create view ", "create table ", "insert into "} {
			if strings.Contains(text, prefix+"information_schema.usage_privileges") {
				c.t.Fatal("serialized builtin source", dump.String())
			}
		}
		old, err := c.conn.QueryContext(c.p.Ctx, upControlQuery)
		if err != nil {
			c.t.Fatal(err)
		}
		defer old.Close()
		saved := upMetadata(c.t, old, false)
		// Whole-catalog replacement retires logical domain-result signatures,
		// even when their external NAME/VARCHAR metadata is unchanged. Assert
		// that fence before preparing a reader in the replacement lifetime.
		reprepare := func() {
			stale := fixture.ErrorDiagnostic(upControlQuery, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
			stale.SelectRows, stale.ErrorStage = true, "execute"
			c.run(held, stale)
			if err := held.Statement.Close(); err != nil {
				c.t.Fatal(err)
			}
			held = c.hold(c.conn, upControlQuery)
		}
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
			c.t.Fatal(err)
		}
		reprepare()
		check(c.conn, nil)
		c.run(held, c.step(upControlQuery, nil))
		c.links(c.conn)
		upAssertRows(c.t, old, want)
		upColumnTypes(c.t, saved, false)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			c.t.Fatal(err)
		}
		reprepare()
		check(c.conn, want)
		c.run(held, c.step(upControlQuery, want))
		c.run(c.conn, c.step("SELECT * FROM public.up_consumer", want))
		c.controlACLs(c.conn)
		c.links(c.conn)
		var again bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &again); err != nil {
			c.t.Fatal(err)
		}
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(again.Bytes())); err != nil {
			c.t.Fatal(err)
		}
		reprepare()
		c.run(held, c.step(upControlQuery, want))
		c.controlACLs(c.conn)
		prefix := "CREATE TABLE up_staged(v integer); INSERT INTO up_staged VALUES("
		err = stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		diagnostic := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		diagnostic.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		fixture.AssertDiagnostic(c.t, err, &diagnostic)
		unchanged := func() {
			c.run(held, c.step(upControlQuery, want))
			c.run(c.conn, fixture.Select("SELECT v FROM public.up_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
			c.run(c.conn, fixture.Select("SELECT relname FROM pg_class WHERE relname='up_staged'", []string{"relname"}, []string{"NAME"}, nil))
			c.controlACLs(c.conn)
		}
		unchanged()
		canceled, cancel := context.WithCancel(c.p.Ctx)
		cancel()
		for _, control := range []func() error{
			func() error { return stuntgres.Reset(canceled, c.p.Native) },
			func() error { return stuntgres.Dump(canceled, c.p.Native, &bytes.Buffer{}) },
			func() error { return stuntgres.Restore(canceled, c.p.Native, bytes.NewReader(dump.Bytes())) },
		} {
			if err := control(); !errors.Is(err, context.Canceled) {
				c.t.Fatal("canceled control", err)
			}
			unchanged()
		}
		connector, err := stuntgres.NewDriver().OpenConnector("up-independent-controls")
		if err != nil {
			c.t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		recorded, recorder := fixture.RecordedNativePool(c.t, connector)
		restored := &upCase{t: c.t, p: c.p, b: fixture.Backend{Name: "destination", DB: recorded, Recorder: recorder}, recorder: recorder}
		for _, role := range upRoles {
			suffix := ""
			if role == "up_power" {
				suffix = " SUPERUSER"
			}
			restored.run(recorded, fixture.Exec("CREATE ROLE "+role+suffix, 0))
		}
		if err := stuntgres.Restore(c.p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			c.t.Fatal(err)
		}
		var database string
		if err := destination.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			c.t.Fatal(err)
		}
		fresh := make([][]any, len(want))
		for i, row := range want {
			fresh[i] = append([]any(nil), row...)
			fresh[i][2] = database
		}
		restored.database = database
		restored.run(recorded, restored.step(upControlQuery, fresh))
		restored.run(recorded, fixture.Prepared(restored.step(upControlQuery, fresh)))
		restored.run(recorded, restored.step("SELECT * FROM public.up_consumer", fresh))
		restored.controlACLs(recorded)
		restored.links(recorded)
	})
}
