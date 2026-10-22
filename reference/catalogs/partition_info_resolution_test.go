package reference_test

import (
	"database/sql"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionInfoNamedCompetition(t *testing.T) {
	partitionInfoScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		partitionInfoSeed(t, p, b, c)
		partitionInfoExec(t, p, b, c,
			"CREATE FUNCTION public.pg_partition_tree(other boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 7$$",
			"CREATE FUNCTION public.pg_partition_ancestors(other boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 8$$",
			"CREATE FUNCTION public.pg_partition_root(other boolean) RETURNS integer LANGUAGE SQL AS $$SELECT 9$$")
		for _, tc := range []struct {
			name, label string
			answer      int64
		}{
			{"pg_partition_tree", "rootrelid", 7}, {"pg_partition_ancestors", "partitionid", 8}, {"pg_partition_root", "", 9},
		} {
			for _, prepared := range []bool{false, true} {
				route := "direct"
				if prepared {
					route = "prepared"
				}
				t.Run(tc.name+"/"+route+"/builtin", func(t *testing.T) {
					argument := "'pinfo_l'::regclass"
					if tc.label != "" {
						argument = tc.label + " => " + argument
					}
					q := "SELECT * FROM " + tc.name + "(" + argument + ")"
					var s fixture.Step
					switch tc.name {
					case "pg_partition_tree":
						s = partitionInfoTree(q, [][]any{{"pinfo_l", "pinfo_m", true, int64(0)}})
					case "pg_partition_ancestors":
						s = fixture.Select(q, []string{"relid"}, []string{"REGCLASS"}, [][]any{{"pinfo_l"}, {"pinfo_m"}, {"pinfo_r"}})
					default:
						s = fixture.Select(q, []string{"pg_partition_root"}, []string{"REGCLASS"}, [][]any{{"pinfo_r"}})
					}
					s.Bag = true
					s.Prepared = prepared
					partitionInfoRun(t, p, b, c, s)
				})
				t.Run(tc.name+"/"+route+"/sql-overload", func(t *testing.T) {
					s := fixture.Select("SELECT "+tc.name+"(other => true)", []string{tc.name}, []string{"INT4"}, [][]any{{tc.answer}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				})
				labels := []string{"wrong"}
				if tc.name == "pg_partition_root" {
					labels = append(labels, "rootrelid", "partitionid")
				}
				for _, label := range labels {
					t.Run(tc.name+"/"+route+"/invalid-label-"+label, func(t *testing.T) {
						q := "SELECT * FROM " + tc.name + "(" + label + " => 'pinfo_l'::regclass)"
						d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.name+"("+label+" => regclass) does not exist")
						d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						d.Position = "15"
						s := fixture.ErrorDiagnostic(q, d)
						s.SelectRows = true
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					})
				}
			}
		}
	})
}

func TestReferencePartitionInfoTextParameters(t *testing.T) {
	partitionInfoScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		partitionInfoSeed(t, p, b, c)
		type consumer struct {
			name, query string
			held        fixture.HeldStatement
		}
		var consumers []consumer
		for _, name := range []string{"pg_partition_tree", "pg_partition_ancestors", "pg_partition_root"} {
			q := "SELECT * FROM " + name + "($1::text)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			consumers = append(consumers, consumer{name, q, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}})
		}
		checkAll := func(label string, input any, tree, ancestors [][]any, root any) {
			t.Helper()
			for _, consumer := range consumers {
				t.Run(consumer.name+"/held-text/"+label, func(t *testing.T) {
					var s fixture.Step
					switch consumer.name {
					case "pg_partition_tree":
						s = partitionInfoTree(consumer.query, tree, input)
					case "pg_partition_ancestors":
						s = fixture.Select(consumer.query, []string{"relid"}, []string{"REGCLASS"}, ancestors, input)
					default:
						s = fixture.Select(consumer.query, []string{"pg_partition_root"}, []string{"REGCLASS"}, [][]any{{root}}, input)
					}
					s.Bag = true
					partitionInfoRun(t, p, b, consumer.held, s)
				})
			}
		}
		leaf := [][]any{{"pinfo_l", "pinfo_m", true, int64(0)}}
		chain := [][]any{{"pinfo_l"}, {"pinfo_m"}, {"pinfo_r"}}
		checkAll("initial-success", "pinfo_l", leaf, chain, "pinfo_r")
		checkAll("null", nil, nil, nil, nil)
		for _, consumer := range consumers {
			// TEXT decodes successfully as a protocol parameter; the implicit
			// REGCLASS coercion, not parameter decoding, rejects this name.
			t.Run(consumer.name+"/held-text/missing", func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42P01", `relation "pinfo_review_missing" does not exist`)
				s := fixture.ErrorDiagnostic(consumer.query, d, "pinfo_review_missing")
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, consumer.held, b.Recorder, s)
			})
		}
		checkAll("same-held-recovery", "pinfo_l", leaf, chain, "pinfo_r")
		partitionInfoExec(t, p, b, c, "CREATE TABLE pinfo_new(k INT,payload TEXT)", "ALTER TABLE pinfo_m ATTACH PARTITION pinfo_new FOR VALUES FROM(5) TO(10)")
		checkAll("attach-refresh", "pinfo_m", [][]any{{"pinfo_m", "pinfo_r", false, int64(0)}, {"pinfo_l", "pinfo_m", true, int64(1)}, {"pinfo_new", "pinfo_m", true, int64(1)}}, [][]any{{"pinfo_m"}, {"pinfo_r"}}, "pinfo_r")
		partitionInfoExec(t, p, b, c, "ALTER TABLE pinfo_r DETACH PARTITION pinfo_m")
		checkAll("detach-refresh", "pinfo_l", leaf, [][]any{{"pinfo_l"}, {"pinfo_m"}}, "pinfo_m")
		checkAll("detached-root", "pinfo_m", [][]any{{"pinfo_m", nil, false, int64(0)}, {"pinfo_l", "pinfo_m", true, int64(1)}, {"pinfo_new", "pinfo_m", true, int64(1)}}, [][]any{{"pinfo_m"}}, "pinfo_m")
		partitionInfoExec(t, p, b, c, "ALTER TABLE pinfo_m DETACH PARTITION pinfo_l")
		checkAll("physical-detach", "pinfo_l", nil, nil, nil)
		for _, name := range []string{"pg_partition_tree", "pg_partition_ancestors", "pg_partition_root"} {
			for _, inputType := range []string{"text", "regclass"} {
				for _, prepared := range []bool{false, true} {
					route := "direct"
					if prepared {
						route = "prepared"
					}
					t.Run(name+"/literal-"+inputType+"/"+route+"/missing", func(t *testing.T) {
						q := "SELECT " + name + "('pinfo_review_missing'::" + inputType + ")"
						d := fixture.ControlsReferenceDiagnostic("42P01", `relation "pinfo_review_missing" does not exist`)
						s := fixture.ErrorDiagnostic(q, d)
						s.SelectRows = true
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if inputType == "regclass" {
							s.Diagnostic.Position = fixture.Position(utf8.RuneCountInString("SELECT "+name+"(") + 1)
							if prepared {
								s.ErrorStage = "prepare"
							}
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					})
				}
				t.Run(name+"/held-"+inputType+"/missing-input-diagnostic", func(t *testing.T) {
					q := "SELECT * FROM " + name + "($1::" + inputType + ")"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					d := fixture.ControlsReferenceDiagnostic("42P01", `relation "pinfo_review_missing" does not exist`)
					if inputType == "regclass" {
						d.Context = "unnamed portal parameter $1 = '...'"
					}
					s := fixture.ErrorDiagnostic(q, d, "pinfo_review_missing")
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
				})
			}
		}
	})
}
