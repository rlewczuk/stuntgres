package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestFTSMatchPreparedOwnership(t *testing.T) {
	t.Run("typed_accepted_generation", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, query := range []string{
			"SELECT 'cat:1 rat:2'::tsvector @@ 'cat<->rat'::tsquery",
			"SELECT 'cat<->rat'::tsquery @@ 'cat:1 rat:2'::tsvector",
			"SELECT to_tsvector('english'::regconfig,'Rats'::text) @@ to_tsquery('english'::regconfig,'rat'::text)",
		} {
			ast, d := sequencePreparedDescription(t, e, s, query)
			if d.Folds == nil || len(d.Folds.Current().Entries) != 0 || d.Folds.Current().Generation != 0 {
				t.Fatal("Describe evaluated match")
			}
			preparedFoldExecute(t, e, s, ast, &d)
			entry := preparedFoldEntry(t, d, sqltype.Bool, "t")
			before := snapshotFoldDescription(d)
			enginetest.SessionSQL(t, s, "SET default_text_search_config='simple'")
			preparedFoldExecute(t, e, s, ast, &d)
			after := d.Folds.Current()
			if after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !samePublishedFold(entry, preparedFoldEntry(t, d, sqltype.Bool, "t")) {
				t.Fatal("accepted BOOL generation replaced")
			}
			convertRejectPublication(t, e, s, ast, d, before.Accepted)
		}
	})
	t.Run("text_live_default", func(t *testing.T) {
		for _, query := range []string{
			"SELECT 'Rats'::text @@ 'rat'::text", "SELECT 'Rats'::text @@ 'rat'::tsquery",
			"SELECT $1::text @@ 'rat'::text", "SELECT $1::text @@ 'rat'::tsquery",
		} {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "SET default_text_search_config='simple'")
			ast, d := sequencePreparedDescription(t, e, s, query)
			for _, tc := range []struct{ setting, want string }{{"english", "t"}, {"simple", "f"}, {"english", "t"}} {
				enginetest.SessionSQL(t, s, "SET default_text_search_config='"+tc.setting+"'")
				convertOwnedExecute(t, e, s, ast, &d, "Rats", tc.want)
				for _, entry := range d.Folds.Current().Entries {
					if entry.Result.Type.ID() == sqltype.Bool {
						t.Fatal("stable match folded", entry)
					}
				}
				if d.Regtypes != nil && len(d.Regtypes.Constants) != 0 {
					t.Fatal("default captured regconfig")
				}
			}
		}
	})
	t.Run("binding_graphs_and_frames", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "BEGIN")
		cycle := s.NewProtocolCycle()
		command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("match_definition", "SELECT ('cat'::tsvector @@ 'cat'::tsquery) AND ($1::tsvector @@ 'cat'::tsquery)", nil)
			return err
		})
		bind := func(name, input string) {
			command(func(scope *core.StatementScope) error {
				data := []byte{0, 0, 0, 1, 0, 0, 0, byte(len(input))}
				data = append(data, []byte(input)...)
				data = append(data, 0, 0)
				_, err := scope.BindProtocolPortal(name, "match_definition", &protocolUnitInput{data: data})
				return err
			})
		}
		bind("match_old", "cat")
		registry := core.PayloadOf(s).PreparedSQL
		definition := registry.Lookup("match_definition")
		owner := definition.Description.Folds
		if owner != nil && len(owner.Current().Entries) != 0 {
			t.Fatal("parameter frame entered persistent definition")
		}
		old := registry.Binding("match_old")
		if old == nil || old.Resource == nil || old.Resource.Root == nil || old.Folds == nil || len(old.Folds.Entries) == 0 {
			t.Fatal("missing private binding artifacts")
		}
		oldRoot, oldResource, accepted := old.Resource.Root, old.Resource, *old.Folds
		oldFolds := old.Folds
		bind("match_new", "dog")
		fresh := registry.Binding("match_new")
		if fresh.Resource == nil || fresh.Resource.Root == nil || fresh.Resource == oldResource || fresh.Resource.Root == oldRoot || fresh.Folds == nil || &fresh.Folds.Entries[0] == &accepted.Entries[0] {
			t.Fatal("bindings shared private graph/frame artifacts")
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("match_definition") })
		for _, tc := range []struct{ name, want string }{{"match_old", "t"}, {"match_new", "f"}} {
			rows := 0
			command(func(scope *core.StatementScope) error {
				_, err := scope.ExecuteProtocolPortal(tc.name, 0, func(fields []session.ProtocolField) error {
					rows++
					if len(fields) != 1 || fields[0].Null || string(fields[0].Bytes) != tc.want {
						t.Fatal("accepted match portal row", tc.name, fields)
					}
					return nil
				}, nil, nil, core.CopyProtocolHooks{})
				return err
			})
			if rows != 1 {
				t.Fatal("accepted match portal cardinality", tc.name, rows)
			}
		}
		if accepted.Generation != oldFolds.Generation || &accepted.Entries[0] != &oldFolds.Entries[0] {
			t.Fatal("execution mutated accepted artifacts")
		}
		for _, name := range []string{"match_old", "match_new"} {
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
		}
		if !old.Retired || !fresh.Retired || oldResource.Root != nil {
			t.Fatal("portal close retained live graph")
		}
		if accepted.Entries[0].Value.IsNull() {
			t.Fatal("retirement mutated accepted value")
		}
		op, _, err := s.AcquireCommand(context.Background())
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
