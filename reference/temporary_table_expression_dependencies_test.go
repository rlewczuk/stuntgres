package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

type temporaryExpressionObjects struct{ provider, table, generation, external, index int64 }

func temporaryExpressionSetup(s *tempReferenceSession, prefix, table, onCommit string, indexed bool) temporaryExpressionObjects {
	s.t.Helper()
	s.utility("CREATE TEMP TABLE "+prefix+"_provider(v INT)"+onCommit, nil)
	persistence := "TEMP "
	if strings.HasPrefix(table, "public.") {
		persistence = ""
	}
	s.exec(s.c, "CREATE "+persistence+"TABLE "+table+"(a INT,g BOOL GENERATED ALWAYS AS('"+prefix+"_provider'::regclass IS NOT NULL) STORED,z INT UNIQUE)", 0)
	var objects temporaryExpressionObjects
	basename := table[strings.LastIndexByte(table, '.')+1:]
	indexName := basename + "_z_key"
	if indexed {
		s.exec(s.c, "CREATE INDEX "+prefix+"_external ON "+table+"(('"+prefix+"_provider'::regclass IS NOT NULL))", 0)
		s.exec(s.c, "CREATE INDEX "+prefix+"_z_idx ON "+table+"(z)", 0)
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
		if err := s.c.QueryRowContext(s.p.ctx, probe.query, probe.args...).Scan(probe.target); err != nil || *probe.target <= 0 || *probe.target > 4294967295 {
			s.t.Fatal(probe.query, probe.args, *probe.target, err)
		}
	}
	q := "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2"
	if err := s.c.QueryRowContext(s.p.ctx, q, objects.table).Scan(&objects.generation); err != nil || objects.generation <= 0 {
		s.t.Fatal(q, objects.table, objects.generation, err)
	}
	temporaryExpressionCatalog(s, s.c, objects, true)
	return objects
}

func temporaryExpressionCatalog(s *tempReferenceSession, c referenceExecutor, o temporaryExpressionObjects, present bool) {
	s.t.Helper()
	backend := referenceBackend{db: s.db, pg: s.postgres, recorder: s.record}
	generated, index := [][]any{}, [][]any{}
	if present {
		generated = [][]any{pgDependEdge(2604, o.generation, 0, 1259, o.table, 2, "i"), pgDependEdge(2604, o.generation, 0, 1259, o.provider, 0, "n")}
		index = [][]any{pgDependEdge(1259, o.external, 0, 1259, o.table, 0, "a"), pgDependEdge(1259, o.external, 0, 1259, o.provider, 0, "n")}
	}
	steps := []referenceStep{pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", generated, o.generation)}
	if o.external != 0 {
		steps = append(steps, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1", index, o.external))
	}
	for _, step := range steps {
		step.prepared = s.prepared
		pgDescriptionExecutorRows(s.t, s.p, backend, c, step)
	}
	if !present {
		for _, id := range []int64{o.provider, o.external} {
			if id == 0 {
				continue
			}
			step := referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{}, id)
			step.prepared = s.prepared
			pgDescriptionExecutorRows(s.t, s.p, backend, c, step)
		}
		step := referenceSelect("SELECT oid FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{}, o.generation)
		step.prepared = s.prepared
		pgDescriptionExecutorRows(s.t, s.p, backend, c, step)
	}
	step := referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{{o.index}}, o.index)
	step.prepared = s.prepared
	pgDescriptionExecutorRows(s.t, s.p, backend, c, step)
}

func temporaryExpressionRows(s *tempReferenceSession, c referenceExecutor, table string, generated bool, values ...[2]int64) {
	s.t.Helper()
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
	step := referenceSelect("SELECT * FROM "+table+" ORDER BY a", columns, types, rows)
	step.prepared = s.prepared
	referenceRunStep(s.t, s.p.ctx, c, s.record, step)
}

func temporaryExpressionFrozen(s *tempReferenceSession, c *sql.Conn, table string) *sql.Rows {
	s.t.Helper()
	if s.postgres {
		return nil
	}
	q := "SELECT * FROM " + table + " ORDER BY a /* retained pre-cascade result */"
	ctx := context.WithValue(s.p.ctx, referenceRecorderKey{}, s.record)
	var rows *sql.Rows
	var err error
	if s.prepared {
		held := s.held(c, q)
		rows, err = held.statement.QueryContext(ctx)
	} else {
		rows, err = c.QueryContext(ctx, q)
	}
	if err != nil {
		s.t.Fatal(q, err)
	}
	s.t.Cleanup(func() {
		if err := rows.Close(); err != nil {
			s.t.Error(err)
		}
	})
	return rows
}

func temporaryExpressionConsumeFrozen(s *tempReferenceSession, c *sql.Conn, table string, rows *sql.Rows, values ...[2]int64) {
	s.t.Helper()
	if rows == nil {
		return
	}
	want := make([][]any, 0, len(values))
	for _, value := range values {
		want = append(want, []any{value[0], true, value[1]})
	}
	q := "SELECT * FROM " + table + " ORDER BY a /* retained pre-cascade result */"
	step := referenceSelect(q, []string{"a", "g", "z"}, []string{"INT4", "BOOL", "INT4"}, want)
	referenceRunStep(s.t, s.p.ctx, sequenceFrozenRows{referenceExecutor: c, rows: rows, query: q}, s.record, step)
}

func temporaryExpressionRoundtrip(s *tempReferenceSession, table, prefix string, values ...[2]int64) {
	s.t.Helper()
	if s.postgres {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
		s.t.Fatal(err)
	}
	for _, absent := range []string{prefix + "_provider", prefix + "_external", "pg_temp_"} {
		if strings.Contains(dump.String(), absent) {
			s.t.Fatal("removed/TEMP dependency leaked", dump.String())
		}
	}
	if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	temporaryExpressionRows(s, s.peer, table, false, values...)
}

func TestReferenceTemporaryTableExpressionCommitDrop(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.exec(s.c, "BEGIN", 0)
		objects := temporaryExpressionSetup(s, "action_expr", "action_survivor", " ON COMMIT DROP", false)
		s.utility("CREATE TEMP TABLE action_clear(a INT,g BOOL GENERATED ALWAYS AS('action_expr_provider'::regclass IS NOT NULL) STORED,z INT UNIQUE) ON COMMIT DELETE ROWS", nil)
		cleared := temporaryExpressionObjects{provider: objects.provider}
		for _, probe := range []struct {
			query  string
			target *int64
		}{
			{"SELECT oid FROM pg_catalog.pg_class WHERE relname='action_clear'", &cleared.table},
			{"SELECT oid FROM pg_catalog.pg_class WHERE relname='action_clear_z_key'", &cleared.index},
		} {
			if err := s.c.QueryRowContext(s.p.ctx, probe.query).Scan(probe.target); err != nil || *probe.target <= 0 {
				s.t.Fatal(probe.query, *probe.target, err)
			}
		}
		if err := s.c.QueryRowContext(s.p.ctx, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", cleared.table).Scan(&cleared.generation); err != nil || cleared.generation <= 0 {
			s.t.Fatal("action_clear generation", cleared.table, cleared.generation, err)
		}
		temporaryExpressionCatalog(s, s.c, cleared, true)
		s.exec(s.c, "INSERT INTO action_clear(a,z) VALUES(21,23)", 1)
		temporaryExpressionRows(s, s.c, "action_clear", true, [2]int64{21, 23})
		s.exec(s.c, "INSERT INTO action_survivor(a,z) VALUES(7,9),(8,10)", 2)
		s.exec(s.c, "DELETE FROM action_survivor WHERE a=8", 1)
		temporaryExpressionRows(s, s.c, "action_survivor", true, [2]int64{7, 9})
		s.exec(s.c, "COMMIT", 0)
		temporaryExpressionCatalog(s, s.c, objects, false)
		temporaryExpressionCatalog(s, s.c, cleared, false)
		temporaryExpressionRows(s, s.c, "action_clear", false)
		temporaryExpressionRows(s, s.c, "action_survivor", false, [2]int64{7, 9})
		s.exec(s.c, "INSERT INTO action_survivor(a,z) VALUES(11,13)", 1)
		temporaryExpressionRows(s, s.c, "action_survivor", false, [2]int64{7, 9}, [2]int64{11, 13})
		step := referenceSelect("SELECT a,z FROM action_survivor WHERE z=13", []string{"a", "z"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(13)}})
		step.prepared = s.prepared
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
		if !s.postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
				s.t.Fatal(err)
			}
			if strings.Contains(dump.String(), "action_") {
				s.t.Fatal("TEMP output leaked", dump.String())
			}
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			temporaryExpressionRows(s, s.c, "action_survivor", false, [2]int64{7, 9}, [2]int64{11, 13})
		}
	})
}

func TestReferenceTemporaryTableExpressionClose(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		objects := temporaryExpressionSetup(s, "lifetime_expr", "public.lifetime_survivor", "", true)
		s.exec(s.c, "INSERT INTO public.lifetime_survivor(a,z) VALUES(7,9),(8,10)", 2)
		s.exec(s.c, "CREATE TABLE public.lifetime_unrelated(v INT)", 0)
		s.exec(s.c, "INSERT INTO public.lifetime_unrelated VALUES(99)", 1)
		temporaryExpressionRows(s, s.peer, "public.lifetime_survivor", true, [2]int64{7, 9}, [2]int64{8, 10})
		frozen := temporaryExpressionFrozen(s, s.peer, "public.lifetime_survivor")
		s.exec(s.peer, "CREATE TEMP TABLE lifetime_expr_provider(v INT)", 0)
		s.exec(s.peer, "INSERT INTO lifetime_expr_provider VALUES(22)", 1)
		s.retire()
		temporaryExpressionConsumeFrozen(s, s.peer, "public.lifetime_survivor", frozen, [2]int64{7, 9}, [2]int64{8, 10})
		temporaryExpressionCatalog(s, s.peer, objects, false)
		temporaryExpressionRows(s, s.peer, "public.lifetime_survivor", false, [2]int64{7, 9}, [2]int64{8, 10})
		s.exec(s.peer, "INSERT INTO public.lifetime_survivor(a,z) VALUES(11,13)", 1)
		s.exec(s.peer, "UPDATE public.lifetime_survivor SET z=19 WHERE a=7", 1)
		temporaryExpressionRows(s, s.peer, "public.lifetime_survivor", false, [2]int64{7, 19}, [2]int64{8, 10}, [2]int64{11, 13})
		temporaryTableRows(s, s.peer, "lifetime_expr_provider", 22)
		temporaryTableRows(s, s.peer, "public.lifetime_unrelated", 99)
		temporaryExpressionRoundtrip(s, "public.lifetime_survivor", "lifetime_expr", [2]int64{7, 19}, [2]int64{8, 10}, [2]int64{11, 13})
		temporaryTableRows(s, s.peer, "lifetime_expr_provider", 22)
		temporaryTableRows(s, s.peer, "public.lifetime_unrelated", 99)
	})
}

func TestReferenceTemporaryTableExplicitDropProjection(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		objects := temporaryExpressionSetup(s, "projection_expr", "projection_survivor", "", true)
		s.exec(s.c, "INSERT INTO projection_survivor(a,z) VALUES(7,9),(8,10)", 2)
		temporaryExpressionRows(s, s.c, "projection_survivor", true, [2]int64{7, 9}, [2]int64{8, 10})
		frozen := temporaryExpressionFrozen(s, s.c, "projection_survivor")
		for _, rollback := range []bool{true, false} {
			s.exec(s.c, "BEGIN", 0)
			s.exec(s.c, "INSERT INTO projection_survivor(a,z) VALUES(11,13)", 1)
			s.exec(s.c, "UPDATE projection_survivor SET z=z+100 WHERE a=7", 1)
			s.exec(s.c, "DELETE FROM projection_survivor WHERE a=8", 1)
			s.exec(s.c, "SAVEPOINT before_cascade", 0)
			s.utility("DROP TABLE projection_expr_provider CASCADE", nil)
			temporaryExpressionCatalog(s, s.c, objects, false)
			temporaryExpressionRows(s, s.c, "projection_survivor", false, [2]int64{7, 109}, [2]int64{11, 13})
			if rollback {
				temporaryExpressionConsumeFrozen(s, s.c, "projection_survivor", frozen, [2]int64{7, 9}, [2]int64{8, 10})
				s.exec(s.c, "ROLLBACK TO before_cascade", 0)
				temporaryExpressionCatalog(s, s.c, objects, true)
				temporaryExpressionRows(s, s.c, "projection_survivor", true, [2]int64{7, 109}, [2]int64{11, 13})
				s.utility("DROP TABLE projection_expr_provider CASCADE", nil)
				s.exec(s.c, "ROLLBACK", 0)
				temporaryExpressionCatalog(s, s.c, objects, true)
				temporaryExpressionRows(s, s.c, "projection_survivor", true, [2]int64{7, 9}, [2]int64{8, 10})
			} else {
				s.exec(s.c, "COMMIT", 0)
			}
		}
		s.exec(s.c, "INSERT INTO projection_survivor(a,z) VALUES(14,16)", 1)
		temporaryExpressionRows(s, s.c, "projection_survivor", false, [2]int64{7, 109}, [2]int64{11, 13}, [2]int64{14, 16})
		step := referenceSelect("SELECT a,z FROM projection_survivor WHERE z=13", []string{"a", "z"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(13)}})
		step.prepared = s.prepared
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
		if !s.postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
				s.t.Fatal(err)
			}
			if strings.Contains(dump.String(), "projection_") {
				s.t.Fatal("TEMP output leaked", dump.String())
			}
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			temporaryExpressionRows(s, s.c, "projection_survivor", false, [2]int64{7, 109}, [2]int64{11, 13}, [2]int64{14, 16})
		}
	})
}

func TestReferenceTemporaryTablePrivateDropProjection(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.exec(s.c, "BEGIN", 0)
		objects := temporaryExpressionSetup(s, "private_expr", "private_survivor", "", false)
		s.exec(s.c, "INSERT INTO private_survivor(a,z) VALUES(7,9)", 1)
		s.exec(s.c, "SAVEPOINT before_pending", 0)
		s.exec(s.c, "INSERT INTO private_survivor(a,z) VALUES(8,10)", 1)
		s.utility("DROP TABLE private_expr_provider CASCADE", nil)
		temporaryExpressionCatalog(s, s.c, objects, false)
		temporaryExpressionRows(s, s.c, "private_survivor", false, [2]int64{7, 9}, [2]int64{8, 10})
		s.exec(s.c, "ROLLBACK TO before_pending", 0)
		temporaryExpressionCatalog(s, s.c, objects, true)
		temporaryExpressionRows(s, s.c, "private_survivor", true, [2]int64{7, 9})
		s.exec(s.c, "INSERT INTO private_survivor(a,z) VALUES(11,13)", 1)
		s.utility("DROP TABLE private_expr_provider CASCADE", nil)
		s.exec(s.c, "COMMIT", 0)
		temporaryExpressionCatalog(s, s.c, objects, false)
		temporaryExpressionRows(s, s.c, "private_survivor", false, [2]int64{7, 9}, [2]int64{11, 13})
		s.exec(s.c, "UPDATE private_survivor SET z=19 WHERE a=7", 1)
		temporaryExpressionRows(s, s.c, "private_survivor", false, [2]int64{7, 19}, [2]int64{11, 13})
	})
}

func TestReferenceTemporaryTableAutocommitDropProjection(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		objects := temporaryExpressionSetup(s, "immediate_expr", "immediate_survivor", "", true)
		s.exec(s.c, "INSERT INTO immediate_survivor(a,z) VALUES(7,9),(8,10)", 2)
		temporaryExpressionRows(s, s.c, "immediate_survivor", true, [2]int64{7, 9}, [2]int64{8, 10})
		frozen := temporaryExpressionFrozen(s, s.c, "immediate_survivor")
		s.utility("DROP TABLE immediate_expr_provider CASCADE", nil)
		temporaryExpressionConsumeFrozen(s, s.c, "immediate_survivor", frozen, [2]int64{7, 9}, [2]int64{8, 10})
		temporaryExpressionCatalog(s, s.c, objects, false)
		temporaryExpressionRows(s, s.c, "immediate_survivor", false, [2]int64{7, 9}, [2]int64{8, 10})
		s.exec(s.c, "INSERT INTO immediate_survivor(a,z) VALUES(11,13)", 1)
		s.exec(s.c, "UPDATE immediate_survivor SET z=19 WHERE a=7", 1)
		s.exec(s.c, "DELETE FROM immediate_survivor WHERE a=8", 1)
		temporaryExpressionRows(s, s.c, "immediate_survivor", false, [2]int64{7, 19}, [2]int64{11, 13})
		if !s.postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
				s.t.Fatal(err)
			}
			if strings.Contains(dump.String(), "immediate_") {
				s.t.Fatal("TEMP output leaked", dump.String())
			}
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			temporaryExpressionRows(s, s.c, "immediate_survivor", false, [2]int64{7, 19}, [2]int64{11, 13})
		}
	})
}
