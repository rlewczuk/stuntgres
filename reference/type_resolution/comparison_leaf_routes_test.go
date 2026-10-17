package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Use a real default/check catalog value. pg_node_tree_in deliberately rejects
// arbitrary input, and physical PostgreSQL node serialization is not a golden.
func TestReferenceComparisonNodeTreeLeaf(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE comparison_node_leaf(v integer DEFAULT 7 CHECK(v>0))", 0))
		node := "(SELECT adbin FROM pg_attrdef WHERE adrelid='comparison_node_leaf'::regclass AND adnum=1)"
		check := "(SELECT conbin FROM pg_constraint WHERE conrelid='comparison_node_leaf'::regclass AND contype='c')"
		for _, value := range []string{node, check} {
			for op, spelling := range comparisonSpellings {
				comparisonCheckpointBool(t, p, b, "ROW("+value+") "+spelling+" ROW("+value+")", comparisonTruth(op, 0))
				comparisonCheckpointBool(t, p, b, "ROW("+value+",NULL::integer) "+spelling+" ROW("+value+",NULL::integer)", nil)
				record := "(SELECT ROW(" + value + ",NULL::integer))"
				comparisonCheckpointBool(t, p, b, record+" "+spelling+" "+record, comparisonTruth(op, 0))
			}
			comparisonCheckpointBool(t, p, b, "ROW("+value+") IS NOT DISTINCT FROM ROW("+value+")", true)
		}
	})
}

func TestReferenceComparisonRecordUnknownFormals(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TYPE comparison_unknown_record AS (a integer,b integer)", 0))
		for _, record := range []string{"ROW(1,2)", "ROW(1,2)::comparison_unknown_record", "NULL::record"} {
			for _, spelling := range comparisonSpellings {
				comparisonCheckpointBool(t, p, b, record+" "+spelling+" NULL", nil)
				comparisonCheckpointBool(t, p, b, "NULL "+spelling+" "+record, nil)
				for _, expr := range []string{record + " " + spelling + " '(1,2)'", "'(1,2)' " + spelling + " " + record} {
					q := "SELECT " + expr + " AS v"
					comparisonCheckpointAnalysis(t, p, b, q, fixture.Diagnostic{Code: "0A000", Message: "input of anonymous composite types is not implemented", Position: comparisonCheckpointPosition(q, "'(1,2)'")})
				}
			}
		}
	})
}

func TestReferenceComparisonDiagnosticVisibility(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "comparison_diag_schema")
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{
			"CREATE SCHEMA comparison_diag_schema",
			"CREATE TYPE comparison_diag_schema.poison AS (j json)",
			"CREATE TYPE public.poison AS (i integer)",
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		q := "SELECT ARRAY[ROW(NULL)::comparison_diag_schema.poison] = ARRAY[ROW(NULL)::comparison_diag_schema.poison] AS v"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, tc := range []struct{ path, name string }{
			{"comparison_diag_schema,public", "poison"},
			{"public,comparison_diag_schema", "comparison_diag_schema.poison"},
			{"public", "comparison_diag_schema.poison"},
			{"comparison_diag_schema,public", "poison"},
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("SET search_path TO "+tc.path, 0))
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "could not identify an equality operator for type " + tc.name})
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}
