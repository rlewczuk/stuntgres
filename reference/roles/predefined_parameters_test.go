package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePredefinedParametersAndNulls(t *testing.T) {
	predefinedCases(t, []string{"pdef_param_actor"}, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE TABLE public.t(id INT,v TEXT)")
		s.run(fixture.Exec("INSERT INTO public.t VALUES(1,'a'),(2,'b')", 2))
		s.exec("CREATE SEQUENCE public.s START 10")
		// PUBLIC schema USAGE keeps lookup independent of either capability.
		s.exec("GRANT pg_read_all_data,pg_write_all_data TO pdef_param_actor")
		s.actor("pdef_param_actor")
		s.run(fixture.Exec("INSERT INTO public.t VALUES($1,$2)", 1, int64(3), nil))
		read := "SELECT id,v FROM public.t WHERE id=$1 ORDER BY id"
		s.run(fixture.Select(read, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), nil}}, int64(3)))
		s.run(fixture.Select(read, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{}, nil))
		s.run(fixture.Exec("UPDATE public.t SET v=$1 WHERE id=$2", 1, nil, int64(1)))
		s.run(fixture.Exec("DELETE FROM public.t WHERE id=$1", 0, nil))
		predefinedRows(s, "public.t", [][]any{{int64(1), nil}, {int64(2), "b"}, {int64(3), nil}})
		update := "UPDATE public.t SET v=$1 WHERE id=$2"
		heldUpdate := predefinedHeld(s, update)
		s.actor("")
		s.exec("REVOKE pg_read_all_data FROM pdef_param_actor")
		s.actor("pdef_param_actor")
		failure := fixture.RoleReferenceError(update, "42501", "permission denied for table t", "", false, "execute")
		failure.Args = []any{"changed", int64(1)}
		fixture.RunStep(s.t, s.p.Ctx, heldUpdate, s.record, failure)
		s.actor("")
		predefinedRows(s, "public.t", [][]any{{int64(1), nil}, {int64(2), "b"}, {int64(3), nil}})
		s.exec("GRANT pg_read_all_data TO pdef_param_actor")
		s.actor("pdef_param_actor")
		fixture.RunStep(s.t, s.p.Ctx, heldUpdate, s.record, fixture.Exec(update, 1, "changed", int64(1)))
		predefinedRows(s, "public.t", [][]any{{int64(1), "changed"}, {int64(2), "b"}, {int64(3), nil}})
		s.run(fixture.Exec("DELETE FROM public.t WHERE id=$1", 1, int64(2)))
		predefinedRows(s, "public.t", [][]any{{int64(1), "changed"}, {int64(3), nil}})
		next := "SELECT nextval($1)"
		set := "SELECT setval($1,$2,$3)"
		s.run(fixture.Select(next, []string{"nextval"}, []string{"INT8"}, [][]any{{int64(10)}}, "public.s"))
		s.run(fixture.Select(set, []string{"setval"}, []string{"INT8"}, [][]any{{int64(20)}}, "public.s", int64(20), false))
		s.run(fixture.Select(next, []string{"nextval"}, []string{"INT8"}, [][]any{{int64(20)}}, "public.s"))
		// Strict NULL inputs produce typed NULLs without advancing/resetting s.
		s.run(fixture.Select(set, []string{"setval"}, []string{"INT8"}, [][]any{{nil}}, "public.s", nil, true))
		s.run(fixture.Select(set, []string{"setval"}, []string{"INT8"}, [][]any{{nil}}, "public.s", int64(99), nil))
		s.run(fixture.Select(next, []string{"nextval"}, []string{"INT8"}, [][]any{{nil}}, nil))
		state := func() {
			s.run(fixture.Select("SELECT last_value,is_called FROM public.s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(20), true}}))
		}
		state()
		heldNext := predefinedHeld(s, next)
		s.actor("")
		s.exec("REVOKE pg_write_all_data FROM pdef_param_actor")
		s.actor("pdef_param_actor")
		failure = fixture.RoleReferenceError(next, "42501", "permission denied for sequence s", "", false, "execute")
		failure.Args, failure.SelectRows = []any{"public.s"}, true
		fixture.RunStep(s.t, s.p.Ctx, heldNext, s.record, failure)
		state()
		s.actor("")
		s.exec("GRANT pg_write_all_data TO pdef_param_actor")
		s.actor("pdef_param_actor")
		fixture.RunStep(s.t, s.p.Ctx, heldNext, s.record, fixture.Select(next, []string{"nextval"}, []string{"INT8"}, [][]any{{int64(21)}}, "public.s"))
		s.run(fixture.Select("SELECT currval($1)", []string{"currval"}, []string{"INT8"}, [][]any{{int64(21)}}, "public.s"))
		s.run(fixture.Select("SELECT last_value,is_called FROM public.s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(21), true}}))
		s.actor("")
	})
}
