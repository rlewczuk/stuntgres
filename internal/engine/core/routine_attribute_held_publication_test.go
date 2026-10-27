package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineAttributeHeldPublication(t *testing.T) {
	t.Run("StrictVariadicFormals", func(t *testing.T) {
		for _, language := range []string{"SQL", "plpgsql"} {
			t.Run(language, func(t *testing.T) {
				e := rootengine.New()
				s := enginetest.StartSession(t, e, nil)
				body := "SELECT xs::text"
				if language == "plpgsql" {
					body = "BEGIN RETURN xs::text; END"
				}
				enginetest.SessionSQL(t, s, "CREATE FUNCTION attribute_array(VARIADIC xs integer[] DEFAULT '{}'::integer[]) RETURNS text LANGUAGE "+language+" STRICT AS '"+body+"'")
				for _, tc := range []struct {
					call string
					want any
				}{
					{"attribute_array(1,NULL,3)", "{1,NULL,3}"},
					{"attribute_array()", "{}"},
					{"attribute_array(VARIADIC ARRAY[1,NULL,3])", "{1,NULL,3}"},
					{"attribute_array(VARIADIC NULL::integer[])", nil},
				} {
					got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT "+tc.call))
					if !reflect.DeepEqual(got, [][]driver.Value{{tc.want}}) {
						t.Fatal(tc.call, got, tc.want)
					}
				}
			})
		}
	})
	t.Run("OptimizedNullAndReachedAuthority", func(t *testing.T) {
		e := rootengine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{
			"CREATE ROLE attribute_reader",
			"GRANT USAGE ON SCHEMA public TO attribute_reader",
			"CREATE TABLE attribute_input(v integer)",
			"INSERT INTO attribute_input VALUES(NULL)",
			"GRANT SELECT ON attribute_input TO attribute_reader",
			"CREATE FUNCTION attribute_private(integer) RETURNS integer LANGUAGE SQL STRICT AS 'SELECT 7'",
			"SET SESSION AUTHORIZATION attribute_reader",
		} {
			enginetest.SessionSQL(t, s, q)
		}
		optimized, folded := sequencePreparedDescription(t, e, s, preparedFoldPoint+",public.attribute_private(NULL::integer)")
		reached, runtime := sequencePreparedDescription(t, e, s, preparedFoldPoint+",public.attribute_private(v) FROM public.attribute_input")
		preparedFoldExecute(t, e, s, optimized, &folded)
		preparedFoldExecute(t, e, s, reached, &runtime)
		old := snapshotFoldDescription(folded)
		dependencies := append(folded.Analysis.Dependencies[:0:0], folded.Analysis.Dependencies...)
		found := false
		for _, dependency := range dependencies {
			procedure, ok := e.CatalogRead().Procedure(dependency.Ref.Address)
			found = found || ok && procedure.Name() == "attribute_private"
		}
		if !found {
			t.Fatal("optimized strict NULL erased original routine lifetime")
		}
		enginetest.SessionSQL(t, s, "RESET SESSION AUTHORIZATION")
		enginetest.SessionSQL(t, s, "REVOKE EXECUTE ON FUNCTION attribute_private(integer) FROM PUBLIC")
		enginetest.SessionSQL(t, s, "SET SESSION AUTHORIZATION attribute_reader")
		preparedFoldExecute(t, e, s, optimized, &folded)
		result, err := sequencePreparedExecute(e, s, reached, &runtime)
		if result.Rows != nil {
			result.Rows.Close()
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for function attribute_private" {
			t.Fatal("reached table NULL lost live EXECUTE authority", err)
		}
		if !reflect.DeepEqual(old.Analysis.Dependencies, dependencies) {
			t.Fatal("live ACL reanalysis mutated old manifest")
		}
		preparedFoldEntry(t, old, sqltype.Text, "(0.10000000000000002,1)")
		enginetest.SessionSQL(t, s, "RESET SESSION AUTHORIZATION")
	})

	t.Run("DescriptionRevision", func(t *testing.T) {
		e := rootengine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE FUNCTION attribute_held(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'")
		enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
		ast, d := sequencePreparedDescription(t, e, s, preparedFoldPoint+",attribute_held(COALESCE(-(7::integer),0)) AS v")
		preparedFoldExecute(t, e, s, ast, &d)
		old := snapshotFoldDescription(d)
		owner := d.Folds
		enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
		enginetest.SessionSQL(t, s, "ALTER FUNCTION attribute_held(integer) IMMUTABLE STRICT PARALLEL SAFE COST 7 SET application_name='held'")
		if preparedRebindValid(t, e, s, d.Analysis) {
			t.Fatal("changed routine declaration reused old analysis")
		}
		preparedFoldExecute(t, e, s, ast, &d)
		if d.Analysis == old.Analysis || d.Folds != owner {
			t.Fatal("refresh lost analysis/definition ownership")
		}
		accepted := owner.Current()
		if accepted.Generation <= old.Accepted.Generation || &accepted.Entries[0] == &old.Accepted.Entries[0] {
			t.Fatal("attribute revision retained obsolete fold publication")
		}
		preparedFoldEntry(t, old, sqltype.Text, "(0.10000000000000002,1)")
		preparedFoldEntry(t, d, sqltype.Text, "(0.1,1)")
		found := false
		for _, dependency := range d.Analysis.Dependencies {
			current, ok := e.CatalogRead().Procedure(dependency.Ref.Address)
			if ok && current.Name() == "attribute_held" {
				found = true
				if dependency.Revision != current.Revision() || !current.Strict || current.Volatility != expr.Immutable || current.Parallel != 's' || current.Cost != 7 || current.Configuration.Len() != 1 || current.Configuration.At(0) != "application_name=held" {
					t.Fatal("fresh dependency omitted semantic attributes")
				}
			}
		}
		if !found {
			t.Fatal("call dependency missing after fresh analysis")
		}
	})

	t.Run("RegistryAndRejectedPublication", func(t *testing.T) {
		e := rootengine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE FUNCTION attribute_portal(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'")
		enginetest.SessionSQL(t, s, "BEGIN")
		cycle := s.NewProtocolCycle()
		command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("attribute_definition", preparedFoldPoint+",attribute_portal(COALESCE(-(7::integer),0)) AS v", nil)
			return err
		})
		registry := core.PayloadOf(s).PreparedSQL
		definition := registry.Lookup("attribute_definition")
		bind := func(name string) {
			command(func(scope *core.StatementScope) error {
				_, err := scope.BindProtocolPortal(name, "attribute_definition", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
				return err
			})
		}
		bind("attribute_old")
		old := registry.Binding("attribute_old")
		owner := definition.Description.Folds
		accepted := owner.Current()
		oldRoot := old.Resource.Root
		oldCall := oldRoot.Expressions[1].(plan.SQLRoutineExpr)
		if oldCall.Properties().Volatility != expr.Volatile {
			t.Fatal("initial call volatility")
		}
		// A definition planning attempt is not an accepted portal. Keep a
		// real attempt in flight while Bind refreshes that same definition's
		// generation; old portals retain their independently accepted roots.
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			op.Done()
			t.Fatal(err)
		}
		environment, err := e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			e.Mu.Unlock()
			op.Done()
			t.Fatal(err)
		}
		publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, environment, auth.Catalog, nil)
		e.Mu.Unlock()
		op.Done()
		if err != nil || publication == nil {
			t.Fatal("definition publication did not begin", err)
		}
		enginetest.SessionSQL(t, s, "ALTER FUNCTION attribute_portal(integer) STABLE STRICT SECURITY DEFINER PARALLEL RESTRICTED COST 9 SET application_name='portal'")
		bind("attribute_new")
		current := registry.Binding("attribute_new")
		newCall := current.Resource.Root.Expressions[1].(plan.SQLRoutineExpr)
		if current.Entry.Description.Folds != owner || current.Folds.Generation <= accepted.Generation || current.Resource.Root == oldRoot || newCall.Procedure == oldCall.Procedure || newCall.Properties().Volatility != expr.Stable || !newCall.Procedure.SecurityDefiner || !newCall.Procedure.Strict || newCall.Procedure.Parallel != 'r' || newCall.Procedure.Cost != 9 || newCall.Procedure.Configuration.Len() != 1 {
			t.Fatal("held Bind did not publish current attributes/volatility")
		}
		if old.Resource.Root != oldRoot || old.Folds.Generation != accepted.Generation || &old.Folds.Entries[0] != &accepted.Entries[0] || oldCall.Procedure.Strict || oldCall.Procedure.SecurityDefiner || oldCall.Procedure.Volatility != expr.Volatile {
			t.Fatal("refresh mutated accepted old portal or declaration")
		}
		op, err = s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err = e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			op.Done()
			t.Fatal(err)
		}
		environment, err = e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			e.Mu.Unlock()
			op.Done()
			t.Fatal(err)
		}
		err = publication.Accept(op.Context(), auth.Catalog, environment, e.Catalog.State.Epoch())
		e.Mu.Unlock()
		op.Done()
		if !errors.Is(err, ids.ErrCatalogReplaced) || publication.Group.Sealed {
			t.Fatal("superseded definition attempt regained publication rights", err)
		}
		if owner.Current().Generation != current.Folds.Generation || &old.Folds.Entries[0] != &accepted.Entries[0] {
			t.Fatal("rejected publication changed generations/backing")
		}
		for _, name := range []string{"attribute_old", "attribute_new"} {
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("attribute_definition") })
		op, _, err = s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		err = e.SyncProtocolCycle(op, cycle)
		op.Done()
		if err != nil {
			t.Fatal(err)
		}
		enginetest.SessionSQL(t, s, "ROLLBACK")
	})
}
