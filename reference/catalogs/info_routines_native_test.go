package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinesNativeCallbacks(t *testing.T) {
	irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ string) {
		irCheck(t, p, b, c, irProjection(b.PG, "routine_schema='public' AND routine_name IN ('ir_go_handler','ir_go_validator')", nil))
		if b.PG {
			return
		} // Public Go registrations have no PostgreSQL equivalent.
		d := stuntgres.NewDriver()
		if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "ir_go_handler"}, Wrapper: itForeignRead{}}); err != nil {
			t.Fatal(err)
		}
		if err := d.RegisterForeignValidator(stuntgres.ForeignValidatorDefinition{Name: stuntgres.ForeignFunctionName{Schema: "public", Name: "ir_go_validator"}, Validate: func(context.Context, stuntgres.ForeignObjectKind, []stuntgres.ForeignOption) error {
			t.Error("metadata invoked validator")
			return itForeignIO
		}}); err != nil {
			t.Fatal(err)
		}
		connector, err := d.OpenConnector("ir_callbacks")
		if err != nil {
			t.Fatal(err)
		}
		pool, recorder := fixture.RecordedNativePool(t, connector)
		conn, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var db string
		if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&db); err != nil {
			t.Fatal(err)
		}
		native := fixture.Backend{DB: pool, Recorder: recorder}
		var owner, redacted [][]any
		for _, tc := range []struct{ name, inputs, data, udt string }{{"ir_go_handler", "", "fdw_handler", "fdw_handler"}, {"ir_go_validator", "text[],oid", "void", "void"}} {
			row := irRow(db, "public", tc.name, rsuIdentity(t, p, conn, "public", tc.name, tc.inputs), "FUNCTION", tc.data, "pg_catalog", tc.udt, "GO", "", nil, false, true)
			owner = append(owner, row)
			copy := append([]any(nil), row...)
			copy[37] = nil
			redacted = append(redacted, copy)
		}
		where := "routine_schema='public' AND routine_name IN ('ir_go_handler','ir_go_validator')"
		irCheck(t, p, native, conn, irProjection(false, where, owner))
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE ROLE ir_go_reader", 0))
		run(fixture.Exec("SET ROLE ir_go_reader", 0))
		irCheck(t, p, native, conn, irProjection(false, where, redacted))
		run(fixture.Exec("RESET ROLE", 0))
		irCheck(t, p, native, conn, irProjection(false, where, owner))
	})
}
