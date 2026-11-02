package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoAttributesNativeControls(t *testing.T) {
	attrScenario(t, func(c *attrCase) {
		t := c.t
		c.exec("CREATE ROLE attr_owner", "CREATE ROLE attr_reader", "CREATE SCHEMA attr_scope",
			"CREATE DOMAIN attr_scope.required AS integer NOT NULL DEFAULT 9",
			`CREATE TYPE attr_scope.pair AS (id integer,removed text,label varchar(3) COLLATE "C",required attr_scope.required)`,
			"ALTER TYPE attr_scope.pair DROP ATTRIBUTE removed", "CREATE TABLE attr_scope.holder(v attr_scope.pair)",
			"ALTER TYPE attr_scope.pair OWNER TO attr_owner", "REVOKE ALL ON TYPE attr_scope.pair FROM PUBLIC")
		expected := func(database string, labelOrdinal, requiredOrdinal int64) [][]any {
			return [][]any{
				attrPatch(attrRow(database, "attr_scope", "pair", "id", 1, "integer", "pg_catalog", "int4"), map[string]any{"numeric_precision": int64(32), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)}),
				attrPatch(attrRow(database, "attr_scope", "pair", "label", labelOrdinal, "character varying", "pg_catalog", "varchar"), map[string]any{"character_maximum_length": int64(3), "character_octet_length": int64(12), "collation_catalog": database, "collation_schema": "pg_catalog", "collation_name": "C"}),
				attrPatch(attrRow(database, "attr_scope", "pair", "required", requiredOrdinal, "USER-DEFINED", "attr_scope", "required"), map[string]any{"is_nullable": "NO", "numeric_precision": int64(32), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)}),
			}
		}
		where := "udt_schema='attr_scope'"
		want := expected(c.database, 3, 4)
		h := c.hold(where)
		c.held(h, where, nil, want...)
		visibility := func(conn *sql.Conn, recorder *fixture.Recorder, want [][]any) {
			for _, role := range []string{"attr_reader", "attr_owner"} {
				fixture.RunStep(t, c.p.Ctx, conn, recorder, fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
				fixture.RunStep(t, c.p.Ctx, conn, recorder, fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{role, role}}))
				var rows [][]any
				if role == "attr_owner" {
					rows = want
				}
				s := attrProjection(c.b.PG, where, rows)
				fixture.RunStep(t, c.p.Ctx, conn, recorder, s)
				fixture.RunStep(t, c.p.Ctx, conn, recorder, fixture.Prepared(s))
				fixture.RunStep(t, c.p.Ctx, conn, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		}
		visibility(c.c, c.r, want)
		if c.b.PG {
			return // Process-local controls have no PostgreSQL SQL equivalent.
		}
		old, err := c.c.QueryContext(c.p.Ctx, attrProjection(false, where, nil).Query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		types, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if dump.Len() == 0 {
			t.Fatal("empty declaration dump")
		}
		for _, forbidden := range []string{"CREATE VIEW information_schema.attributes", `CREATE VIEW "information_schema"."attributes"`, "INSERT INTO information_schema.attributes"} {
			if strings.Contains(dump.String(), forbidden) {
				t.Fatal("serialized provider rows", forbidden)
			}
		}
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
			t.Fatal(err)
		}
		c.check(where)
		stale := fixture.ErrorDiagnostic(h.SQL, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
		stale.SelectRows, stale.ErrorStage = true, "execute"
		fixture.RunStep(t, c.p.Ctx, h, c.r, stale)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, c.p.Ctx, h, c.r, stale)
		attrConsume(t, old, types, false, want)
		// Dump emits live declarations, not dropped-attribute history. Restore
		// creates a genuinely new physical layout; retained rows stay sparse.
		restored := expected(c.database, 2, 3)
		h = c.hold(where)
		c.held(h, where, nil, restored...)
		visibility(c.c, c.r, restored)

		connector, err := stuntgres.NewDriver().OpenConnector("attributes-independent-controls")
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		wrapped, recorder := fixture.RecordedNativePool(t, connector)
		fc, err := wrapped.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		for _, role := range []string{"attr_owner", "attr_reader"} {
			fixture.RunStep(t, c.p.Ctx, fc, recorder, fixture.Exec("CREATE ROLE "+role, 0))
		}
		if err := stuntgres.Restore(c.p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var freshDB string
		if err := fc.QueryRowContext(c.p.Ctx, "SELECT current_database()").Scan(&freshDB); err != nil {
			t.Fatal(err)
		}
		freshWant := expected(freshDB, 2, 3)
		freshStep := attrProjection(false, where, freshWant)
		fixture.RunStep(t, c.p.Ctx, fc, recorder, freshStep)
		fixture.RunStep(t, c.p.Ctx, fc, recorder, fixture.Prepared(freshStep))
		visibility(fc, recorder, freshWant)
		prefix := "CREATE SCHEMA attr_staged; CREATE TYPE attr_staged.pair AS (v integer); CREATE TABLE attr_staged.t(v integer); INSERT INTO attr_staged.t VALUES("
		d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		d.Position = strconv.Itoa(len(prefix) + 1)
		err = stuntgres.Restore(c.p.Ctx, c.p.Native, strings.NewReader(prefix+"'bad');"))
		fixture.AssertDiagnostic(t, err, &d)
		unchanged := func() {
			c.held(h, where, nil, restored...)
			c.check("udt_schema='attr_staged'")
			visibility(c.c, c.r, restored)
			attrMetadata(t, types, false, attrColumns, attrTypes)
		}
		unchanged()
		ctx, cancel := context.WithCancel(c.p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, c.p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore=%v", err)
		}
		unchanged()
	})
}
