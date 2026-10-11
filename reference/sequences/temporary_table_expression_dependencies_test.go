package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type temporaryExpressionObjects struct{ provider, table, generation, external, index int64 }

func temporaryExpressionSetup(s *fixture.TempReferenceSession, prefix, table, onCommit string, indexed bool) temporaryExpressionObjects {
	s.T.Helper()
	s.Utility("CREATE TEMP TABLE "+prefix+"_provider(v INT)"+onCommit, nil)
	persistence := "TEMP "
	if strings.HasPrefix(table, "public.") {
		persistence = ""
	}
	s.Exec(s.C, "CREATE "+persistence+"TABLE "+table+"(a INT,g BOOL GENERATED ALWAYS AS('"+prefix+"_provider'::regclass IS NOT NULL) STORED,z INT UNIQUE)", 0)
	var objects temporaryExpressionObjects
	basename := table[strings.LastIndexByte(table, '.')+1:]
	indexName := basename + "_z_key"
	if indexed {
		s.Exec(s.C, "CREATE INDEX "+prefix+"_external ON "+table+"(('"+prefix+"_provider'::regclass IS NOT NULL))", 0)
		s.Exec(s.C, "CREATE INDEX "+prefix+"_z_idx ON "+table+"(z)", 0)
		indexName = prefix + "_z_idx"
	}
	probes := []struct {
		query  string
		args   []any
		target *int64
	}{
		{"SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", []any{prefix + "_provider"}, &objects.provider},
		{"SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", []any{basename}, &objects.table},
		{"SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", []any{indexName}, &objects.index},
	}
	if indexed {
		probes = append(probes, struct {
			query  string
			args   []any
			target *int64
		}{"SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", []any{prefix + "_external"}, &objects.external})
	}
	for _, probe := range probes {
		if err := s.C.QueryRowContext(s.P.Ctx, probe.query, probe.args...).Scan(probe.target); err != nil || *probe.target <= 0 || *probe.target > 4294967295 {
			s.T.Fatal(probe.query, probe.args, *probe.target, err)
		}
	}
	q := "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2"
	if err := s.C.QueryRowContext(s.P.Ctx, q, objects.table).Scan(&objects.generation); err != nil || objects.generation <= 0 {
		s.T.Fatal(q, objects.table, objects.generation, err)
	}
	temporaryExpressionCatalog(s, s.C, objects, true)
	return objects
}

func temporaryExpressionCatalog(s *fixture.TempReferenceSession, c fixture.Executor, o temporaryExpressionObjects, present bool) {
	s.T.Helper()
	backend := fixture.Backend{DB: s.DB, PG: s.Postgres, Recorder: s.Record}
	generated, index := [][]any{}, [][]any{}
	if present {
		generated = [][]any{fixture.PgDependEdge(2604, o.generation, 0, 1259, o.table, 2, "i"), fixture.PgDependEdge(2604, o.generation, 0, 1259, o.provider, 0, "n")}
		index = [][]any{fixture.PgDependEdge(1259, o.external, 0, 1259, o.table, 0, "a"), fixture.PgDependEdge(1259, o.external, 0, 1259, o.provider, 0, "n")}
	}
	steps := []fixture.Step{fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", generated, o.generation)}
	if o.external != 0 {
		steps = append(steps, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", index, o.external))
	}
	for _, step := range steps {
		step.Prepared = s.Prepared
		fixture.PgDescriptionExecutorRows(s.T, s.P, backend, c, step)
	}
	if !present {
		for _, id := range []int64{o.provider, o.external} {
			if id == 0 {
				continue
			}
			step := fixture.Select("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{}, id)
			step.Prepared = s.Prepared
			fixture.PgDescriptionExecutorRows(s.T, s.P, backend, c, step)
		}
		step := fixture.Select("SELECT oid FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{}, o.generation)
		step.Prepared = s.Prepared
		fixture.PgDescriptionExecutorRows(s.T, s.P, backend, c, step)
	}
	step := fixture.Select("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{{o.index}}, o.index)
	step.Prepared = s.Prepared
	fixture.PgDescriptionExecutorRows(s.T, s.P, backend, c, step)
}

func temporaryExpressionRows(s *fixture.TempReferenceSession, c fixture.Executor, table string, generated bool, values ...[2]int64) {
	s.T.Helper()
	columns, types := []string{"a", "z"}, []string{"INT4", "INT4"}
	if generated {
		columns, types = []string{"a", "g", "z"}, []string{"INT4", "BOOL", "INT4"}
	}
	rows := make([][]any, 0, len(values))
	for _, value := range values {
		if generated {
			rows = append(rows, []any{value[0], true, value[1]})
		} else {
			rows = append(rows, []any{value[0], value[1]})
		}
	}
	step := fixture.Select("SELECT * FROM "+table+" ORDER BY a", columns, types, rows)
	step.Prepared = s.Prepared
	fixture.RunStep(s.T, s.P.Ctx, c, s.Record, step)
}

func temporaryExpressionFrozen(s *fixture.TempReferenceSession, c *sql.Conn, table string) *sql.Rows {
	s.T.Helper()
	if s.Postgres {
		return nil
	}
	q := "SELECT * FROM " + table + " ORDER BY a /* retained pre-cascade result */"
	ctx := context.WithValue(s.P.Ctx, fixture.RecorderKey{}, s.Record)
	var rows *sql.Rows
	var err error
	if s.Prepared {
		held := s.Held(c, q)
		rows, err = held.Statement.QueryContext(ctx)
	} else {
		rows, err = c.QueryContext(ctx, q)
	}
	if err != nil {
		s.T.Fatal(q, err)
	}
	s.T.Cleanup(func() {
		if err := rows.Close(); err != nil {
			s.T.Error(err)
		}
	})
	return rows
}

func temporaryExpressionConsumeFrozen(s *fixture.TempReferenceSession, c *sql.Conn, table string, rows *sql.Rows, values ...[2]int64) {
	s.T.Helper()
	if rows == nil {
		return
	}
	want := make([][]any, 0, len(values))
	for _, value := range values {
		want = append(want, []any{value[0], true, value[1]})
	}
	q := "SELECT * FROM " + table + " ORDER BY a /* retained pre-cascade result */"
	step := fixture.Select(q, []string{"a", "g", "z"}, []string{"INT4", "BOOL", "INT4"}, want)
	fixture.RunStep(s.T, s.P.Ctx, fixture.SequenceFrozenRows{Executor: c, Rows: rows, Query: q}, s.Record, step)
}

func temporaryExpressionRoundtrip(s *fixture.TempReferenceSession, table, prefix string, values ...[2]int64) {
	s.T.Helper()
	if s.Postgres {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
		s.T.Fatal(err)
	}
	for _, absent := range []string{prefix + "_provider", prefix + "_external", "pg_temp_"} {
		if strings.Contains(dump.String(), absent) {
			s.T.Fatal("removed/TEMP dependency leaked", dump.String())
		}
	}
	if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.T.Fatal(err)
	}
	temporaryExpressionRows(s, s.Peer, table, false, values...)
}

func TestReferenceTemporaryTableExpressionCommitDrop(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Exec(s.C, "BEGIN", 0)
		objects := temporaryExpressionSetup(s, "action_expr", "action_survivor", " ON COMMIT DROP", false)
		s.Utility("CREATE TEMP TABLE action_clear(a INT,g BOOL GENERATED ALWAYS AS('action_expr_provider'::regclass IS NOT NULL) STORED,z INT UNIQUE) ON COMMIT DELETE ROWS", nil)
		cleared := temporaryExpressionObjects{provider: objects.provider}
		for _, probe := range []struct {
			query  string
			target *int64
		}{
			{"SELECT oid FROM pg_catalog.pg_class WHERE relname='action_clear'", &cleared.table},
			{"SELECT oid FROM pg_catalog.pg_class WHERE relname='action_clear_z_key'", &cleared.index},
		} {
			if err := s.C.QueryRowContext(s.P.Ctx, probe.query).Scan(probe.target); err != nil || *probe.target <= 0 {
				s.T.Fatal(probe.query, *probe.target, err)
			}
		}
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", cleared.table).Scan(&cleared.generation); err != nil || cleared.generation <= 0 {
			s.T.Fatal("action_clear generation", cleared.table, cleared.generation, err)
		}
		temporaryExpressionCatalog(s, s.C, cleared, true)
		s.Exec(s.C, "INSERT INTO action_clear(a,z) VALUES(21,23)", 1)
		temporaryExpressionRows(s, s.C, "action_clear", true, [2]int64{21, 23})
		s.Exec(s.C, "INSERT INTO action_survivor(a,z) VALUES(7,9),(8,10)", 2)
		s.Exec(s.C, "DELETE FROM action_survivor WHERE a=8", 1)
		temporaryExpressionRows(s, s.C, "action_survivor", true, [2]int64{7, 9})
		s.Exec(s.C, "COMMIT", 0)
		temporaryExpressionCatalog(s, s.C, objects, false)
		temporaryExpressionCatalog(s, s.C, cleared, false)
		temporaryExpressionRows(s, s.C, "action_clear", false)
		temporaryExpressionRows(s, s.C, "action_survivor", false, [2]int64{7, 9})
		s.Exec(s.C, "INSERT INTO action_survivor(a,z) VALUES(11,13)", 1)
		temporaryExpressionRows(s, s.C, "action_survivor", false, [2]int64{7, 9}, [2]int64{11, 13})
		step := fixture.Select("SELECT a,z FROM action_survivor WHERE z=13", []string{"a", "z"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(13)}})
		step.Prepared = s.Prepared
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
		if !s.Postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			if strings.Contains(dump.String(), "action_") {
				s.T.Fatal("TEMP output leaked", dump.String())
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
			temporaryExpressionRows(s, s.C, "action_survivor", false, [2]int64{7, 9}, [2]int64{11, 13})
		}
	})
}

func TestReferenceTemporaryTableExpressionClose(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		objects := temporaryExpressionSetup(s, "lifetime_expr", "public.lifetime_survivor", "", true)
		s.Exec(s.C, "INSERT INTO public.lifetime_survivor(a,z) VALUES(7,9),(8,10)", 2)
		s.Exec(s.C, "CREATE TABLE public.lifetime_unrelated(v INT)", 0)
		s.Exec(s.C, "INSERT INTO public.lifetime_unrelated VALUES(99)", 1)
		temporaryExpressionRows(s, s.Peer, "public.lifetime_survivor", true, [2]int64{7, 9}, [2]int64{8, 10})
		frozen := temporaryExpressionFrozen(s, s.Peer, "public.lifetime_survivor")
		s.Exec(s.Peer, "CREATE TEMP TABLE lifetime_expr_provider(v INT)", 0)
		s.Exec(s.Peer, "INSERT INTO lifetime_expr_provider VALUES(22)", 1)
		s.Retire()
		temporaryExpressionConsumeFrozen(s, s.Peer, "public.lifetime_survivor", frozen, [2]int64{7, 9}, [2]int64{8, 10})
		temporaryExpressionCatalog(s, s.Peer, objects, false)
		temporaryExpressionRows(s, s.Peer, "public.lifetime_survivor", false, [2]int64{7, 9}, [2]int64{8, 10})
		s.Exec(s.Peer, "INSERT INTO public.lifetime_survivor(a,z) VALUES(11,13)", 1)
		s.Exec(s.Peer, "UPDATE public.lifetime_survivor SET z=19 WHERE a=7", 1)
		temporaryExpressionRows(s, s.Peer, "public.lifetime_survivor", false, [2]int64{7, 19}, [2]int64{8, 10}, [2]int64{11, 13})
		fixture.TemporaryTableRows(s, s.Peer, "lifetime_expr_provider", 22)
		fixture.TemporaryTableRows(s, s.Peer, "public.lifetime_unrelated", 99)
		temporaryExpressionRoundtrip(s, "public.lifetime_survivor", "lifetime_expr", [2]int64{7, 19}, [2]int64{8, 10}, [2]int64{11, 13})
		fixture.TemporaryTableRows(s, s.Peer, "lifetime_expr_provider", 22)
		fixture.TemporaryTableRows(s, s.Peer, "public.lifetime_unrelated", 99)
	})
}

func TestReferenceTemporaryTableExplicitDropProjection(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		objects := temporaryExpressionSetup(s, "projection_expr", "projection_survivor", "", true)
		s.Exec(s.C, "INSERT INTO projection_survivor(a,z) VALUES(7,9),(8,10)", 2)
		temporaryExpressionRows(s, s.C, "projection_survivor", true, [2]int64{7, 9}, [2]int64{8, 10})
		frozen := temporaryExpressionFrozen(s, s.C, "projection_survivor")
		for _, rollback := range []bool{true, false} {
			s.Exec(s.C, "BEGIN", 0)
			s.Exec(s.C, "INSERT INTO projection_survivor(a,z) VALUES(11,13)", 1)
			s.Exec(s.C, "UPDATE projection_survivor SET z=z+100 WHERE a=7", 1)
			s.Exec(s.C, "DELETE FROM projection_survivor WHERE a=8", 1)
			s.Exec(s.C, "SAVEPOINT before_cascade", 0)
			s.Utility("DROP TABLE projection_expr_provider CASCADE", nil)
			temporaryExpressionCatalog(s, s.C, objects, false)
			temporaryExpressionRows(s, s.C, "projection_survivor", false, [2]int64{7, 109}, [2]int64{11, 13})
			if rollback {
				temporaryExpressionConsumeFrozen(s, s.C, "projection_survivor", frozen, [2]int64{7, 9}, [2]int64{8, 10})
				s.Exec(s.C, "ROLLBACK TO before_cascade", 0)
				temporaryExpressionCatalog(s, s.C, objects, true)
				temporaryExpressionRows(s, s.C, "projection_survivor", true, [2]int64{7, 109}, [2]int64{11, 13})
				s.Utility("DROP TABLE projection_expr_provider CASCADE", nil)
				s.Exec(s.C, "ROLLBACK", 0)
				temporaryExpressionCatalog(s, s.C, objects, true)
				temporaryExpressionRows(s, s.C, "projection_survivor", true, [2]int64{7, 9}, [2]int64{8, 10})
			} else {
				s.Exec(s.C, "COMMIT", 0)
			}
		}
		s.Exec(s.C, "INSERT INTO projection_survivor(a,z) VALUES(14,16)", 1)
		temporaryExpressionRows(s, s.C, "projection_survivor", false, [2]int64{7, 109}, [2]int64{11, 13}, [2]int64{14, 16})
		step := fixture.Select("SELECT a,z FROM projection_survivor WHERE z=13", []string{"a", "z"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(13)}})
		step.Prepared = s.Prepared
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
		if !s.Postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			if strings.Contains(dump.String(), "projection_") {
				s.T.Fatal("TEMP output leaked", dump.String())
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
			temporaryExpressionRows(s, s.C, "projection_survivor", false, [2]int64{7, 109}, [2]int64{11, 13}, [2]int64{14, 16})
		}
	})
}

func TestReferenceTemporaryTablePrivateDropProjection(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Exec(s.C, "BEGIN", 0)
		objects := temporaryExpressionSetup(s, "private_expr", "private_survivor", "", false)
		s.Exec(s.C, "INSERT INTO private_survivor(a,z) VALUES(7,9)", 1)
		s.Exec(s.C, "SAVEPOINT before_pending", 0)
		s.Exec(s.C, "INSERT INTO private_survivor(a,z) VALUES(8,10)", 1)
		s.Utility("DROP TABLE private_expr_provider CASCADE", nil)
		temporaryExpressionCatalog(s, s.C, objects, false)
		temporaryExpressionRows(s, s.C, "private_survivor", false, [2]int64{7, 9}, [2]int64{8, 10})
		s.Exec(s.C, "ROLLBACK TO before_pending", 0)
		temporaryExpressionCatalog(s, s.C, objects, true)
		temporaryExpressionRows(s, s.C, "private_survivor", true, [2]int64{7, 9})
		s.Exec(s.C, "INSERT INTO private_survivor(a,z) VALUES(11,13)", 1)
		s.Utility("DROP TABLE private_expr_provider CASCADE", nil)
		s.Exec(s.C, "COMMIT", 0)
		temporaryExpressionCatalog(s, s.C, objects, false)
		temporaryExpressionRows(s, s.C, "private_survivor", false, [2]int64{7, 9}, [2]int64{11, 13})
		s.Exec(s.C, "UPDATE private_survivor SET z=19 WHERE a=7", 1)
		temporaryExpressionRows(s, s.C, "private_survivor", false, [2]int64{7, 19}, [2]int64{11, 13})
	})
}

func TestReferenceTemporaryTableAutocommitDropProjection(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		objects := temporaryExpressionSetup(s, "immediate_expr", "immediate_survivor", "", true)
		s.Exec(s.C, "INSERT INTO immediate_survivor(a,z) VALUES(7,9),(8,10)", 2)
		temporaryExpressionRows(s, s.C, "immediate_survivor", true, [2]int64{7, 9}, [2]int64{8, 10})
		frozen := temporaryExpressionFrozen(s, s.C, "immediate_survivor")
		s.Utility("DROP TABLE immediate_expr_provider CASCADE", nil)
		temporaryExpressionConsumeFrozen(s, s.C, "immediate_survivor", frozen, [2]int64{7, 9}, [2]int64{8, 10})
		temporaryExpressionCatalog(s, s.C, objects, false)
		temporaryExpressionRows(s, s.C, "immediate_survivor", false, [2]int64{7, 9}, [2]int64{8, 10})
		s.Exec(s.C, "INSERT INTO immediate_survivor(a,z) VALUES(11,13)", 1)
		s.Exec(s.C, "UPDATE immediate_survivor SET z=19 WHERE a=7", 1)
		s.Exec(s.C, "DELETE FROM immediate_survivor WHERE a=8", 1)
		temporaryExpressionRows(s, s.C, "immediate_survivor", false, [2]int64{7, 19}, [2]int64{11, 13})
		if !s.Postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			if strings.Contains(dump.String(), "immediate_") {
				s.T.Fatal("TEMP output leaked", dump.String())
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
			temporaryExpressionRows(s, s.C, "immediate_survivor", false, [2]int64{7, 19}, [2]int64{11, 13})
		}
	})
}
