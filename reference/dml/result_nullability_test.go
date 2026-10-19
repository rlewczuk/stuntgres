package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceResultNullabilitySelect(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := fixture.IdentityConnection(t, p.Ctx, backend.DB)
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			run := func(s fixture.Step) { fixture.RunStep(t, ctx, c, backend.Recorder, s) }
			run(fixture.Exec("SET search_path TO public", 0))
			run(fixture.Exec("CREATE TABLE public.result_nullability(id INT NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO public.result_nullability VALUES(1)", 1))
			q := "SELECT " + resultNullabilityProjection + " FROM public.result_nullability WHERE id=$1::int AND $2::bool"
			held := fixture.IdentityHeld(t, ctx, c, q)
			metadata, retained := resultNullabilityMetadata(t, backend.PG, resultNullabilityNames, resultNullabilityTypes, resultNullabilityPairs(false))
			for _, prepared := range []bool{false, true} {
				var x fixture.Executor = c
				if prepared {
					x = held
				}
				for _, frame := range []struct {
					path string
					args []any
					rows [][]any
				}{
					{"public", []any{1, true, false, 403, "can_order", "SELECT"}, [][]any{resultNullabilityValues(t, backend, 1, "public", "{public}", false, true, "=r/0")}},
					{"public", []any{1, true, nil, nil, "can_order", nil}, [][]any{resultNullabilityValues(t, backend, 1, "public", nil, nil, nil, nil)}},
					{"''", []any{1, true, false, 403, "not_a_property", nil}, [][]any{resultNullabilityValues(t, backend, 1, nil, "{}", false, nil, nil)}},
					{"''", []any{1, true, true, 403, "can_order", "SELECT"}, [][]any{resultNullabilityValues(t, backend, 1, nil, "{pg_catalog}", false, true, "=r/0")}},
					{"''", []any{1, false, nil, nil, "can_order", nil}, [][]any{}},
				} {
					run(fixture.Exec("SET search_path TO "+frame.path, 0))
					s := fixture.Select(q, resultNullabilityNames, resultNullabilityTypes, frame.rows, frame.args...)
					s.CheckMetadata = metadata
					fixture.RunStep(t, ctx, x, backend.Recorder, s)
					retained()
				}
			}
			// SELECT must never change the fixture, including after empty-path reuse.
			run(fixture.Select("SELECT id FROM public.result_nullability", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}

func TestReferenceResultNullabilityReturning(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := fixture.IdentityConnection(t, p.Ctx, backend.DB)
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			run := func(s fixture.Step) { fixture.RunStep(t, ctx, c, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE public.result_nullability(id INT NOT NULL)", 0))
			for _, command := range []string{"INSERT", "UPDATE", "DELETE", "MERGE"} {
				projection := "id AS target_id,old.id AS old_id,new.id AS new_id," + resultNullabilityProjection
				names := append([]string{"target_id", "old_id", "new_id"}, resultNullabilityNames...)
				types := append([]string{"INT4", "INT4", "INT4"}, resultNullabilityTypes...)
				nn, n := resultNullabilityPair{false, true}, resultNullabilityPair{true, true}
				prefix := []resultNullabilityPair{nn, nn, nn}
				var q string
				switch command {
				case "INSERT":
					q = "INSERT INTO public.result_nullability SELECT $1::int WHERE $2::bool"
					prefix[1] = n
				case "UPDATE":
					q = "UPDATE public.result_nullability SET id=id WHERE id=$1::int AND $2::bool"
				case "DELETE":
					q = "DELETE FROM public.result_nullability WHERE id=$1::int AND $2::bool"
					prefix[2] = n
				case "MERGE":
					q = "MERGE INTO public.result_nullability t USING (VALUES($1::int)) s(sid) ON t.id=s.sid WHEN MATCHED AND $2::bool THEN UPDATE SET id=t.id"
					prefix = []resultNullabilityPair{nn, n, n}
					projection += ",merge_action() AS action"
					names = append(names, "action")
					types = append(types, "TEXT")
				}
				q += " RETURNING " + projection
				pairs := append(prefix, resultNullabilityPairs(true)...)
				if command == "MERGE" {
					pairs = append(pairs, nn)
				}
				held := fixture.IdentityHeld(t, ctx, c, q)
				metadata, retained := resultNullabilityMetadata(t, backend.PG, names, types, pairs)
				for _, prepared := range []bool{false, true} {
					var x fixture.Executor = c
					if prepared {
						x = held
					}
					for frame := range 3 {
						// Explicitly restore state; changed keys and NULL arguments must not
						// leak between executions of the exact same prepared handle.
						run(fixture.Exec("SET search_path TO public", 0))
						run(fixture.Exec("TRUNCATE public.result_nullability", 0))
						id := 10 + frame
						if command != "INSERT" {
							run(fixture.Exec("INSERT INTO public.result_nullability VALUES($1)", 1, id))
						}
						var schema, paths, other, property, acl any = "public", "{public}", false, true, "=r/0"
						args := []any{id, frame != 2, false, 403, "can_order", "SELECT"}
						if frame == 2 && (command == "UPDATE" || command == "DELETE") {
							args[0], args[1] = 999, true
						}
						if frame == 1 {
							run(fixture.Exec("SET search_path TO ''", 0))
							schema, paths, other, property, acl = nil, nil, nil, nil, nil
							args[2], args[3], args[5] = nil, nil, nil
						}
						want := [][]any{}
						if frame != 2 {
							var old, new any = int64(id), int64(id)
							if command == "INSERT" {
								old = nil
							}
							if command == "DELETE" {
								new = nil
							}
							values := append([]any{int64(id), old, new}, resultNullabilityValues(t, backend, id, schema, paths, other, property, acl)...)
							if command == "MERGE" {
								values = append(values, "UPDATE")
							}
							want = [][]any{values}
						}
						s := fixture.Select(q, names, types, want, args...)
						s.CheckMetadata = metadata
						fixture.RunStep(t, ctx, x, backend.Recorder, s)
						retained()
						effects := [][]any{{int64(id)}}
						if command == "INSERT" && frame == 2 || command == "DELETE" && frame != 2 {
							effects = [][]any{}
						}
						run(fixture.Select("SELECT id FROM public.result_nullability", []string{"id"}, []string{"INT4"}, effects))
					}
				}
			}
		})
	}
}
