package reference_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGProcControls(t *testing.T) {
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	pgProcExec(t, p, native, native.DB,
		"CREATE DOMAIN pp_control_int AS integer",
		"CREATE FUNCTION pp_control(a integer DEFAULT 7) RETURNS integer LANGUAGE SQL STABLE STRICT PARALLEL SAFE SET application_name='pp_control' RETURN a",
		"CREATE FUNCTION pp_control_domain(a pp_control_int) RETURNS pp_control_int LANGUAGE SQL RETURN a",
		"CREATE FUNCTION pp_control(text) RETURNS text LANGUAGE SQL AS 'SELECT $1'",
		"CREATE PROCEDURE pp_control_proc(OUT answer integer) LANGUAGE SQL AS 'SELECT 9'")
	dump := fixture.ViewDump(t, p)
	if len(dump) == 0 {
		t.Fatal("empty actual native Dump")
	}
	const q = "SELECT proname,prosrc,pg_get_expr(proargdefaults,0) AS defaults,prosqlbody IS NOT NULL AS body FROM pg_proc WHERE oid='pp_control(integer)'::regprocedure"
	check := func(t *testing.T, b fixture.Backend, x fixture.Executor) {
		t.Helper()
		pgProcBoth(t, p, b, x, "SELECT p.proname,p.proargtypes[0]='integer'::regtype::oid AS input,p.prorettype='integer'::regtype::oid AS result,p.proowner=r.oid AS owner,n.nspname,l.lanname,p.prokind,p.provolatile,p.proisstrict,p.proparallel,p.proconfig,p.prosrc,p.prosqlbody IS NOT NULL AS body,p.proargdefaults IS NOT NULL AS defaults FROM pg_proc p JOIN pg_roles r ON r.rolname=current_user JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE p.oid='pp_control(integer)'::regprocedure", []string{"proname", "input", "result", "owner", "nspname", "lanname", "prokind", "provolatile", "proisstrict", "proparallel", "proconfig", "prosrc", "body", "defaults"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "NAME", "NAME", "CHAR", "CHAR", "BOOL", "CHAR", "_TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"pp_control", true, true, true, "public", "sql", "f", "s", true, "s", "{application_name=pp_control}", "", true, true}})
		pgProcBoth(t, p, b, x, "SELECT proargtypes[0]='pp_control_int'::regtype::oid AS input,prorettype='pp_control_int'::regtype::oid AS result FROM pg_proc WHERE oid='pp_control_domain(pp_control_int)'::regprocedure", []string{"input", "result"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		pgProcBoth(t, p, b, x, "SELECT pg_get_expr(proargdefaults,0) AS defaults FROM pg_proc WHERE oid='pp_control(integer)'::regprocedure", []string{"defaults"}, []string{"TEXT"}, [][]any{{"7"}})
	}
	// The generated domain OID cannot be a cross-server golden. Its overload
	// membership is checked separately using independent regtype identities.
	checkMembership := func(t *testing.T, b fixture.Backend, x fixture.Executor) {
		t.Helper()
		pgProcBoth(t, p, b, x, "SELECT count(*) AS n FROM pg_proc WHERE proname='pp_control'", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}})
		pgProcBoth(t, p, b, x, "SELECT proargtypes,prorettype,prokind,prosrc FROM pg_proc WHERE oid='pp_control(text)'::regprocedure", []string{"proargtypes", "prorettype", "prokind", "prosrc"}, []string{"OIDVECTOR", "OID", "CHAR", "TEXT"}, [][]any{{"25", int64(25), "f", "SELECT $1"}})
		pgProcBoth(t, p, b, x, "SELECT pp_control() AS n,pp_control('x'::text) AS s", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "x"}})
		pgProcBoth(t, p, b, x, "CALL pp_control_proc(NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(9)}})
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := c.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("PG rejected unchanged actual native Dump", err)
				}
				check(t, b, c)
				checkMembership(t, b, c)
				return
			}
			old, err := b.DB.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			check(t, b, b.DB)
			checkMembership(t, b, b.DB)
			const countSQL = "SELECT count(*) AS n FROM pg_proc WHERE proname IN ('pp_control','pp_control_domain','pp_control_proc')"
			countStmt, err := b.DB.PrepareContext(p.Ctx, countSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer countStmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: countStmt, SQL: countSQL}
			count := func(n int64) {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(countSQL, []string{"n"}, []string{"INT8"}, [][]any{{n}}))
			}
			count(4)
			broken := append(append([]byte(nil), dump...), []byte("CREATE TABLE pp_control_bad(x integer,x integer);")...)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(broken)); err == nil {
				t.Fatal("invalid staged Restore succeeded")
			} else {
				d := fixture.ControlsReferenceDiagnostic("42701", `column "x" specified more than once`)
				fixture.AssertDiagnostic(t, err, &d)
			}
			count(4)
			check(t, b, b.DB)
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			checkMembership(t, b, b.DB)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			pgProcBoth(t, p, b, b.DB, "SELECT count(*) AS n FROM pg_proc WHERE proname IN ('pp_control','pp_control_proc')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			count(0)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgProcShapeStep(b, "SELECT * FROM pg_proc WHERE oid=1397", [][]any{pgProcAbsRow()}))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check(t, b, b.DB)
			checkMembership(t, b, b.DB)
			count(4)
			pgProcRetained(t, old, metadata, "pp_control", "7")
			if redump := fixture.ViewDump(t, p); !bytes.Equal(dump, redump) {
				t.Fatal("logical routine Dump changed after Restore")
			}
		})
	}
	pgProcNativeCallback(t, p)
}

func pgProcNativeCallback(t *testing.T, p *fixture.Pair) {
	t.Helper()
	// Go registration is a native API boundary, not a pretend SQL/C language or
	// an executable PostgreSQL fixture. The real callback must not be invoked.
	d := stuntgres.NewDriver()
	calls := 0
	if err := d.RegisterForeignValidator(stuntgres.ForeignValidatorDefinition{Name: stuntgres.ForeignFunctionName{Schema: "public", Name: "pp_control_validator"}, Validate: func(context.Context, stuntgres.ForeignObjectKind, []stuntgres.ForeignOption) error {
		calls++
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	connector, err := d.OpenConnector("pg_proc_native_callback")
	if err != nil {
		t.Fatal(err)
	}
	pool, recorder := fixture.RecordedNativePool(t, connector)
	b := fixture.Backend{DB: pool, Recorder: recorder}
	pgProcBoth(t, p, b, pool, "SELECT prolang,prosrc,probin,prokind,proargtypes,prorettype,proowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owner FROM pg_proc WHERE proname='pp_control_validator'", []string{"prolang", "prosrc", "probin", "prokind", "proargtypes", "prorettype", "owner"}, []string{"OID", "TEXT", "TEXT", "CHAR", "OIDVECTOR", "OID", "BOOL"}, [][]any{{int64(0), "", nil, "f", "1009 26", int64(2278), true}})
	if calls != 0 {
		t.Fatalf("catalog read invoked real native validator %d times", calls)
	}
}
