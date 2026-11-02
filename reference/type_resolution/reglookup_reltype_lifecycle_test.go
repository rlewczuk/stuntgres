package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func regLookupRelTypeLiveChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	regObjectExec(t, p, b, c, "CREATE SCHEMA rl_left", "CREATE SCHEMA rl_right",
		"CREATE TABLE rl_left.same(v integer)", "CREATE TABLE rl_right.same(v integer)",
		"CREATE DOMAIN rl_left.d AS varchar(12)", "CREATE DOMAIN rl_right.d AS integer",
		"SET search_path TO rl_left,rl_right,public")
	left := regLookupIdentity(t, p, c, `SELECT 'rl_left.same'::regclass::oid::bigint`)
	right := regLookupIdentity(t, p, c, `SELECT 'rl_right.same'::regclass::oid::bigint`)
	leftType := regLookupIdentity(t, p, c, `SELECT 'rl_left.d'::regtype::oid::bigint`)
	rightType := regLookupIdentity(t, p, c, `SELECT 'rl_right.d'::regtype::oid::bigint`)
	namespace := regLookupIdentity(t, p, c, `SELECT 'rl_left'::regnamespace::oid::bigint`)
	if left == right || leftType == rightType {
		t.Fatal("distinct definitions share an identity")
	}
	literalSQL := `SELECT to_regclass('same')::oid::bigint AS c,to_regtype('d')::oid::bigint AS t,to_regtypemod('d') AS m,to_regnamespace('rl_left')::oid::bigint AS n`
	parameterSQL := `SELECT to_regclass($1)::oid::bigint AS c,to_regtype($2)::oid::bigint AS t,to_regtypemod($2) AS m,to_regnamespace($3)::oid::bigint AS n`
	literal := regLookupHeld(t, p, c, literalSQL)
	defer literal.Statement.Close()
	parameter := regLookupHeld(t, p, c, parameterSQL)
	defer parameter.Statement.Close()
	regObjectExec(t, p, b, c, "CREATE VIEW public.rl_live_view AS "+literalSQL,
		`CREATE TABLE public.rl_live_defaults(id integer,c regclass DEFAULT to_regclass('same'),t regtype DEFAULT to_regtype('d'),m integer DEFAULT to_regtypemod('d'),n regnamespace DEFAULT to_regnamespace('rl_left'))`)
	id := int64(0)
	checkpoint := func(class, typ, mod any) {
		t.Helper()
		values := [][]any{{class, typ, mod, namespace}}
		for _, route := range []struct {
			held fixture.HeldStatement
			args []any
		}{{literal, nil}, {parameter, []any{"same", "d", "rl_left"}}} {
			s := fixture.Select(route.held.SQL, []string{"c", "t", "m", "n"}, []string{"INT8", "INT8", "INT4", "INT8"}, values, route.args...)
			regObjectBoth(t, p, b, c, s)
			regObjectRunStep(t, p, b, route.held, s)
		}
		regObjectBoth(t, p, b, c, fixture.Select("SELECT c,t,m,n FROM public.rl_live_view", []string{"c", "t", "m", "n"}, []string{"INT8", "INT8", "INT4", "INT8"}, values))
		id++
		regObjectRunStep(t, p, b, c, fixture.Exec("INSERT INTO public.rl_live_defaults(id) VALUES($1)", 1, id))
		regObjectBoth(t, p, b, c, fixture.Select("SELECT c::oid::bigint AS c,t::oid::bigint AS t,m,n::oid::bigint AS n FROM public.rl_live_defaults WHERE id=$1", []string{"c", "t", "m", "n"}, []string{"INT8", "INT8", "INT4", "INT8"}, values, id))
	}
	checkpoint(left, leftType, int64(-1))
	regObjectExec(t, p, b, c, "SET search_path TO rl_right,rl_left,public")
	checkpoint(right, rightType, int64(-1))
	regObjectBoth(t, p, b, c, fixture.Select(`SELECT to_regclass('rl_left.same')::oid::bigint AS c,to_regtype('rl_left.d')::oid::bigint AS t`, []string{"c", "t"}, []string{"INT8", "INT8"}, [][]any{{left, leftType}}))
	regObjectExec(t, p, b, c, "SET search_path TO rl_left,public", "ALTER TABLE rl_left.same RENAME TO changed", "ALTER DOMAIN rl_left.d RENAME TO changed_d")
	checkpoint(nil, nil, nil)
	regObjectBoth(t, p, b, c, fixture.Select(`SELECT to_regclass('rl_left.changed')::oid::bigint AS c,to_regtype('rl_left.changed_d')::oid::bigint AS t`, []string{"c", "t"}, []string{"INT8", "INT8"}, [][]any{{left, leftType}}))
	// TEXT operands create no referent dependency in held/view/default recipes.
	regObjectExec(t, p, b, c, "DROP TABLE rl_left.changed", "DROP DOMAIN rl_left.changed_d")
	checkpoint(nil, nil, nil)
	regObjectExec(t, p, b, c, "CREATE TABLE rl_left.same(v integer)", "CREATE DOMAIN rl_left.d AS varchar(12)")
	replacement := regLookupIdentity(t, p, c, `SELECT 'rl_left.same'::regclass::oid::bigint`)
	replacementType := regLookupIdentity(t, p, c, `SELECT 'rl_left.d'::regtype::oid::bigint`)
	if replacement == left || replacementType == leftType {
		t.Fatal("runtime lookup reused a dropped identity")
	}
	checkpoint(replacement, replacementType, int64(-1))
	regObjectExec(t, p, b, c, "CREATE TABLE rl_left.pg_class(v integer)", "CREATE DOMAIN rl_left.int4 AS integer")
	shadowClass := regLookupIdentity(t, p, c, `SELECT 'rl_left.pg_class'::regclass::oid::bigint`)
	shadowType := regLookupIdentity(t, p, c, `SELECT 'rl_left.int4'::regtype::oid::bigint`)
	for _, tc := range []struct {
		path       string
		class, typ int64
	}{
		{"rl_left,public", 1259, 23},
		{"rl_left,pg_catalog", shadowClass, shadowType},
		{"pg_catalog,rl_left", 1259, 23},
	} {
		regObjectExec(t, p, b, c, "SET search_path TO "+tc.path)
		regObjectBoth(t, p, b, c, fixture.Select(`SELECT to_regclass('pg_class')::oid::bigint AS c,to_regtype('int4')::oid::bigint AS t`, []string{"c", "t"}, []string{"INT8", "INT8"}, [][]any{{tc.class, tc.typ}}))
	}
	regObjectExec(t, p, b, c, "CREATE TEMP TABLE rl_temp(v integer)")
	regObjectBoth(t, p, b, c, fixture.Select(`SELECT to_regclass('rl_temp')::oid='pg_temp.rl_temp'::regclass::oid AS c,to_regtype('rl_temp')::oid='pg_temp.rl_temp'::regtype::oid AS t,to_regnamespace('pg_temp') AS n`, []string{"c", "t", "n"}, []string{"BOOL", "BOOL", "REGNAMESPACE"}, [][]any{{true, true, nil}}))
	regObjectExec(t, p, b, c, "DROP TABLE pg_temp.rl_temp")
	regObjectExec(t, p, b, c, "DROP VIEW public.rl_live_view", "DROP TABLE public.rl_live_defaults", "SET search_path TO public")
}

func TestReferenceRegLookupRelTypeLifecycle(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		regLookupScenario(t, []string{"rl_left", "rl_right"}, nil, regLookupRelTypeLiveChecks)
	})
	t.Run("authorization", func(t *testing.T) {
		regLookupScenario(t, []string{"rl_private", "rl_reader"}, []string{"rl_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			regObjectExec(t, p, b, c, "CREATE SCHEMA rl_private", "CREATE TABLE rl_private.secret(v integer)", "CREATE DOMAIN rl_private.secret_type AS integer", "CREATE ROLE rl_reader", "SET search_path TO rl_private,public", "SET ROLE rl_reader")
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, `SELECT to_regclass('secret') AS c,to_regtype('secret_type') AS t,to_regtypemod('secret_type') AS m,to_regnamespace('rl_private') AS n`, []string{"c", "t", "m", "n"}, []string{"REGCLASS", "REGTYPE", "INT4", "REGNAMESPACE"}, [][]any{{nil, nil, nil, "rl_private"}}))
			for _, tc := range []struct {
				name, input, kind, valid string
				output                   any
			}{
				{"to_regclass", "rl_private.secret", "REGCLASS", "pg_catalog.pg_class", "pg_class"},
				{"to_regtype", "rl_private.secret_type", "REGTYPE", "integer", "integer"},
				{"to_regtypemod", "rl_private.secret_type", "INT4", "integer", int64(-1)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					regLookupRelTypeErrorRoutes(t, p, b, c, tc.name, tc.input, tc.kind, tc.valid, tc.output, fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema rl_private"))
				})
			}
			regObjectExec(t, p, b, c, "RESET ROLE", "GRANT USAGE ON SCHEMA rl_private TO rl_reader", "SET ROLE rl_reader")
			// No SELECT privilege is granted on the relation.
			regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, `SELECT to_regclass('rl_private.secret') AS c,to_regtype('rl_private.secret_type') AS t,to_regtypemod('rl_private.secret_type') AS m`, []string{"c", "t", "m"}, []string{"REGCLASS", "REGTYPE", "INT4"}, [][]any{{"secret", "secret_type", int64(-1)}}))
			regObjectExec(t, p, b, c, "RESET ROLE")
			regObjectExec(t, p, b, c, "CREATE SCHEMA rl_reader AUTHORIZATION rl_reader", "CREATE TABLE rl_reader.owned(v integer)", "CREATE DOMAIN rl_reader.owned_type AS varchar(12)", `SET search_path TO "$user",public`, "SET ROLE rl_reader")
			regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, `SELECT to_regclass('owned') AS c,to_regtype('owned_type') AS t,to_regtypemod('owned_type') AS m,to_regnamespace(current_user::text) AS n`, []string{"c", "t", "m", "n"}, []string{"REGCLASS", "REGTYPE", "INT4", "REGNAMESPACE"}, [][]any{{"owned", "owned_type", int64(-1), "rl_reader"}}))
			regObjectExec(t, p, b, c, "RESET ROLE")
		})
	})
	t.Run("atomicity-cancellation", func(t *testing.T) {
		regLookupScenario(t, nil, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			regObjectExec(t, p, b, c, `CREATE TABLE public.rl_atomic(id integer,m integer DEFAULT to_regtypemod('varchar(0)'))`)
			regObjectRunStep(t, p, b, c, fixture.Exec("INSERT INTO public.rl_atomic(id,m) VALUES(0,42)", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO public.rl_atomic(id,m) VALUES(1,16),(2,DEFAULT)", fixture.ControlsReferenceDiagnostic("22023", "length for type varchar must be at least 1"))
				s.Prepared = prepared
				regObjectRunStep(t, p, b, c, s)
				regObjectBoth(t, p, b, c, fixture.Select("SELECT id,m FROM public.rl_atomic ORDER BY id", []string{"id", "m"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(42)}}))
			}
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				rows, err := c.QueryContext(ctx, "SELECT to_regtypemod('varchar(12)')")
				if rows != nil {
					rows.Close()
					t.Error("canceled lookup exposed rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("canceled lookup", err)
				}
				if _, ok := err.(interface{ SQLState() string }); ok {
					t.Fatal("ordinary cancellation acquired SQLSTATE", err)
				}
			} else {
				// The fixture observes real server admission; never race a fast
				// lookup against a timer and call that cancellation coverage.
				fixture.SessionCancelQuery(t, p.Ctx, c, false, "INSERT INTO public.rl_atomic(id,m) SELECT 3,to_regtypemod('varchar(12)') FROM pg_sleep(600)", false)
			}
			regObjectBoth(t, p, b, c, fixture.Select("SELECT id,m FROM public.rl_atomic ORDER BY id", []string{"id", "m"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(42)}}))
		})
	})
}
