package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRuleRoutineAuthority(t *testing.T) {
	objectGrantRuleAuthority(t, false)
}

func TestReferenceObjectGrantRuleDefaultManifest(t *testing.T) {
	objectGrantRuleAuthority(t, true)
}

// The action's relation access belongs to the source owner, but its callable
// authority belongs to the initiating actor. The DEFAULT variant deliberately
// has no routine call in the action: NEW.n must carry the original recipe.
func objectGrantRuleAuthority(t *testing.T, defaults bool) {
	t.Helper()
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.json_array_length(json)"})
		objectGrantConsumerACL(run)
		source, sink := "rg_src", "rg_sink"
		declaration := "CREATE TABLE rg_src(id integer,j json)"
		action := "INSERT INTO rg_sink VALUES(NEW.id,pg_catalog.json_array_length(NEW.j))"
		query := "INSERT INTO rg_src VALUES($1,'[1,2]'::json)"
		if defaults {
			source, sink = "rg_default_src", "rg_default_sink"
			declaration = "CREATE TABLE rg_default_src(id integer,n integer DEFAULT pg_catalog.json_array_length('[1,2]'::json))"
			action = "INSERT INTO rg_default_sink VALUES(NEW.id,NEW.n)"
			query = "INSERT INTO rg_default_src(id) VALUES($1)"
		}
		run(fixture.Exec(declaration, 0))
		run(fixture.Exec("CREATE TABLE "+sink+"(id integer,n integer)", 0))
		run(fixture.Exec("CREATE RULE rg_action AS ON INSERT TO "+source+" DO ALSO "+action, 0))
		run(fixture.Exec("GRANT INSERT ON "+sink+" TO og_object_other", 0))
		objectGrantConsumerOwner(run, source)
		run(fixture.Exec("GRANT INSERT ON "+source+" TO og_object_reader", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		identity := func() { run(objectGrantConsumerIdentity()) }
		insert := func(first int64) {
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Exec(query, 1, first))
				identity()
				objectGrantStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, first+1))
				identity()
			})
		}
		read := func(ids ...int64) {
			rows := make([][]any, len(ids))
			for i, id := range ids {
				rows[i] = []any{id, int64(2)}
			}
			run(fixture.Select("SELECT id,n FROM "+sink+" ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, rows))
			if defaults {
				run(fixture.Select("SELECT id,n FROM "+source+" ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, rows))
			} else {
				for i, id := range ids {
					rows[i] = []any{id, "[1,2]"}
				}
				run(fixture.Select("SELECT id,j FROM "+source+" ORDER BY id", []string{"id", "j"}, []string{"INT4", "JSON"}, rows))
			}
		}
		insert(1)
		read(1, 2)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM og_object_reader", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_other", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			denied := objectGrantConsumerDenied(query, "", int64(3))
			run(denied)
			identity()
			denied.Args = []any{int64(4)}
			objectGrantStep(t, p.Ctx, held, b.Recorder, denied)
			identity()
		})
		read(1, 2)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM og_object_other", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_reader", 0))
		insert(5)
		read(1, 2, 5, 6)
	})
}

func objectGrantConsumerACL(run func(fixture.Step)) {
	run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader,og_object_other", 0))
	run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC,og_object_reader,og_object_other", 0))
}

func objectGrantConsumerOwner(run func(fixture.Step), table string) {
	run(fixture.Exec("GRANT CREATE ON SCHEMA public TO og_object_other", 0))
	run(fixture.Exec("ALTER TABLE "+table+" OWNER TO og_object_other", 0))
	run(fixture.Exec("REVOKE CREATE ON SCHEMA public FROM og_object_other", 0))
}

func objectGrantConsumerIdentity() fixture.Step {
	return fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"og_object_reader"}})
}

func objectGrantConsumerDenied(query, context string, args ...any) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function json_array_length", Context: context}, args...)
	s.ErrorStage = "execute"
	return s
}
