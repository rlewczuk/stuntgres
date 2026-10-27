package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePredefinedParametersAndNulls(t *testing.T) {
	predefinedCases(t, []string{"pdef_param_actor"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("CREATE TABLE public.t(id INT,v TEXT)")
		s.Run(fixture.Exec("INSERT INTO public.t VALUES(1,'a'),(2,'b')", 2))
		s.Exec("CREATE SEQUENCE public.s START 10")
		// PUBLIC schema USAGE keeps lookup independent of either capability.
		s.Exec("GRANT pg_read_all_data,pg_write_all_data TO pdef_param_actor")
		s.Actor("pdef_param_actor")
		s.Run(fixture.Exec("INSERT INTO public.t VALUES($1,$2)", 1, int64(3), nil))
		read := "SELECT id,v FROM public.t WHERE id=$1 ORDER BY id"
		s.Run(fixture.Select(read, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), nil}}, int64(3)))
		s.Run(fixture.Select(read, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{}, nil))
		s.Run(fixture.Exec("UPDATE public.t SET v=$1 WHERE id=$2", 1, nil, int64(1)))
		s.Run(fixture.Exec("DELETE FROM public.t WHERE id=$1", 0, nil))
		predefinedRows(s, "public.t", [][]any{{int64(1), nil}, {int64(2), "b"}, {int64(3), nil}})
		update := "UPDATE public.t SET v=$1 WHERE id=$2"
		heldUpdate := predefinedHeld(s, update)
		s.Actor("")
		s.Exec("REVOKE pg_read_all_data FROM pdef_param_actor")
		s.Actor("pdef_param_actor")
		failure := fixture.RoleReferenceError(update, "42501", "permission denied for table t", "", false, "execute")
		failure.Args = []any{"changed", int64(1)}
		fixture.RunStep(s.T, s.P.Ctx, heldUpdate, s.Record, failure)
		s.Actor("")
		predefinedRows(s, "public.t", [][]any{{int64(1), nil}, {int64(2), "b"}, {int64(3), nil}})
		s.Exec("GRANT pg_read_all_data TO pdef_param_actor")
		s.Actor("pdef_param_actor")
		fixture.RunStep(s.T, s.P.Ctx, heldUpdate, s.Record, fixture.Exec(update, 1, "changed", int64(1)))
		predefinedRows(s, "public.t", [][]any{{int64(1), "changed"}, {int64(2), "b"}, {int64(3), nil}})
		s.Run(fixture.Exec("DELETE FROM public.t WHERE id=$1", 1, int64(2)))
		predefinedRows(s, "public.t", [][]any{{int64(1), "changed"}, {int64(3), nil}})
		next := "SELECT nextval($1)"
		set := "SELECT setval($1,$2,$3)"
		s.Run(fixture.Select(next, []string{"nextval"}, []string{"INT8"}, [][]any{{int64(10)}}, "public.s"))
		s.Run(fixture.Select(set, []string{"setval"}, []string{"INT8"}, [][]any{{int64(20)}}, "public.s", int64(20), false))
		s.Run(fixture.Select(next, []string{"nextval"}, []string{"INT8"}, [][]any{{int64(20)}}, "public.s"))
		// Strict NULL inputs produce typed NULLs without advancing/resetting s.
		s.Run(fixture.Select(set, []string{"setval"}, []string{"INT8"}, [][]any{{nil}}, "public.s", nil, true))
		s.Run(fixture.Select(set, []string{"setval"}, []string{"INT8"}, [][]any{{nil}}, "public.s", int64(99), nil))
		s.Run(fixture.Select(next, []string{"nextval"}, []string{"INT8"}, [][]any{{nil}}, nil))
		state := func() {
			s.Run(fixture.Select("SELECT last_value,is_called FROM public.s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(20), true}}))
		}
		state()
		heldNext := predefinedHeld(s, next)
		s.Actor("")
		s.Exec("REVOKE pg_write_all_data FROM pdef_param_actor")
		s.Actor("pdef_param_actor")
		failure = fixture.RoleReferenceError(next, "42501", "permission denied for sequence s", "", false, "execute")
		failure.Args, failure.SelectRows = []any{"public.s"}, true
		fixture.RunStep(s.T, s.P.Ctx, heldNext, s.Record, failure)
		state()
		s.Actor("")
		s.Exec("GRANT pg_write_all_data TO pdef_param_actor")
		s.Actor("pdef_param_actor")
		fixture.RunStep(s.T, s.P.Ctx, heldNext, s.Record, fixture.Select(next, []string{"nextval"}, []string{"INT8"}, [][]any{{int64(21)}}, "public.s"))
		s.Run(fixture.Select("SELECT currval($1)", []string{"currval"}, []string{"INT8"}, [][]any{{int64(21)}}, "public.s"))
		s.Run(fixture.Select("SELECT last_value,is_called FROM public.s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(21), true}}))
		s.Actor("")
	})
}
