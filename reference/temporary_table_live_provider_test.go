package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func temporaryLiveProviderID(s *tempReferenceSession, q string, args ...any) int64 {
	s.t.Helper()
	var id int64
	if err := s.c.QueryRowContext(s.p.ctx, q, args...).Scan(&id); err != nil || id <= 0 || id > 4294967295 {
		s.t.Fatal(q, args, id, err)
	}
	return id
}

func temporaryLiveProviderRun(s *tempReferenceSession, step referenceStep) {
	step.prepared = s.prepared
	pgDescriptionExecutorRows(s.t, s.p, referenceBackend{db: s.db, pg: s.postgres, recorder: s.record}, s.c, step)
}

func temporaryLiveProviderDump(s *tempReferenceSession, names ...string) []byte {
	s.t.Helper()
	if s.postgres {
		return nil
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
		s.t.Fatal(err)
	}
	for _, name := range append(names, "pg_temp_") {
		if strings.Contains(dump.String(), name) {
			s.t.Fatal("live TEMP leaked into persistent dump", name, dump.String())
		}
	}
	return dump.Bytes()
}

func TestReferenceTemporaryTableLiveProviderControls(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.exec(s.c, "CREATE TABLE public.live_kept(v INT)", 0)
		s.exec(s.c, "INSERT INTO public.live_kept VALUES(71)", 1)
		s.utility("CREATE TEMP TABLE live_provider(v INT)", nil)
		s.utility("CREATE TEMP TABLE live_consumer(a INT,g BOOL GENERATED ALWAYS AS('live_provider'::regclass IS NOT NULL) STORED,z INT)", nil)
		s.exec(s.c, "INSERT INTO live_provider VALUES(31)", 1)
		s.exec(s.c, "INSERT INTO live_consumer(a,z) VALUES(7,9)", 1)
		namespace := temporaryLiveProviderID(s, "SELECT relnamespace FROM pg_catalog.pg_class WHERE relname=$1", "live_provider")
		s.exec(s.peer, "CREATE TEMP TABLE live_provider(v INT)", 0)
		s.exec(s.peer, "CREATE TEMP TABLE live_consumer(a INT,g BOOL GENERATED ALWAYS AS('live_provider'::regclass IS NOT NULL) STORED,z INT)", 0)
		s.exec(s.peer, "INSERT INTO live_provider VALUES(81)", 1)
		s.exec(s.peer, "INSERT INTO live_consumer(a,z) VALUES(81,82)", 1)
		provider := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1 AND relnamespace=$2", "live_provider", namespace)
		consumer := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1 AND relnamespace=$2", "live_consumer", namespace)
		generation := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", consumer)
		catalog := func() {
			temporaryLiveProviderRun(s, referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE relname=$1 AND relnamespace=$2", []string{"oid"}, []string{"OID"}, [][]any{{provider}}, "live_provider", namespace))
			temporaryLiveProviderRun(s, referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE relname=$1 AND relnamespace=$2", []string{"oid"}, []string{"OID"}, [][]any{{consumer}}, "live_consumer", namespace))
			temporaryLiveProviderRun(s, referenceSelect("SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", []string{"oid"}, []string{"OID"}, [][]any{{generation}}, consumer))
			temporaryLiveProviderRun(s, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", [][]any{pgDependEdge(2604, generation, 0, 1259, consumer, 2, "i"), pgDependEdge(2604, generation, 0, 1259, provider, 0, "n")}, generation))
			temporaryLiveProviderRun(s, referenceSelect("SELECT pg_get_expr(adbin,adrelid) AS definition FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"definition"}, []string{"TEXT"}, [][]any{{"('live_provider'::regclass IS NOT NULL)"}}, generation))
		}
		rows := func(pairs ...[2]int64) {
			temporaryExpressionRows(s, s.c, "live_consumer", true, pairs...)
			temporaryExpressionRows(s, s.peer, "live_consumer", true, [2]int64{81, 82})
			temporaryTableRows(s, s.c, "live_provider", 31)
			temporaryTableRows(s, s.peer, "live_provider", 81)
		}
		catalog()
		rows([2]int64{7, 9})
		held := s.held(s.c, "INSERT INTO live_consumer(a,z) VALUES($1,$2)")
		dump := temporaryLiveProviderDump(s, "live_provider", "live_consumer")
		if !s.postgres {
			if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
				s.t.Fatal(err)
			}
		}
		// Minimal generated-only consumer reaches the global generation scan during
		// unrelated persistent DDL, with its provider still live and unchanged.
		s.utility("CREATE TABLE public.after_live_reset(v INT)", nil)
		catalog()
		rows([2]int64{7, 9})
		referenceRunStep(s.t, s.p.ctx, held, s.record, referenceExec(held.query, 1, 11, 13))
		s.utility("ALTER TABLE live_consumer ALTER COLUMN z SET DEFAULT 17", nil)
		s.exec(s.c, "INSERT INTO live_consumer(a) VALUES(15)", 1)
		catalog()
		rows([2]int64{7, 9}, [2]int64{11, 13}, [2]int64{15, 17})
		if !s.postgres {
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump)); err != nil {
				s.t.Fatal(err)
			}
		}
		s.utility("CREATE TABLE public.after_live_restore(v INT)", nil)
		catalog()
		rows([2]int64{7, 9}, [2]int64{11, 13}, [2]int64{15, 17})
		s.exec(s.c, "UPDATE live_consumer SET z=19 WHERE a=7", 1)
		s.utility("ALTER TABLE live_consumer ALTER COLUMN z SET DEFAULT 23", nil)
		s.exec(s.c, "INSERT INTO live_consumer(a) VALUES(21)", 1)
		rows([2]int64{7, 19}, [2]int64{11, 13}, [2]int64{15, 17}, [2]int64{21, 23})
		catalog()
		temporaryLiveProviderDump(s, "live_provider", "live_consumer")
		temporaryTableRows(s, s.c, "public.live_kept", 71)
	})
}

func TestReferenceTemporaryTableLiveRelationProviderContexts(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.utility("CREATE TEMP TABLE context_provider(v INT)", nil)
		s.exec(s.c, "CREATE INDEX context_provider_index ON context_provider(v)", 0)
		s.utility("CREATE TEMP TABLE context_consumer(a INT,d BOOL DEFAULT ('context_provider'::regclass IS NOT NULL),g BOOL GENERATED ALWAYS AS('context_provider_index'::regclass IS NOT NULL) STORED,z INT,CONSTRAINT context_check CHECK('context_provider_index'::regclass IS NOT NULL))", nil)
		s.exec(s.c, "CREATE INDEX context_derived ON context_consumer(('context_provider'::regclass IS NOT NULL))", 0)
		s.exec(s.c, "INSERT INTO context_consumer(a,z) VALUES(7,9)", 1)
		provider := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname='context_provider'")
		providerIndex := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname='context_provider_index'")
		consumer := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname='context_consumer'")
		derived := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname='context_derived'")
		defaultID := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", consumer)
		generation := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=3", consumer)
		check := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='context_check'", consumer)
		catalog := func() {
			for _, id := range []int64{provider, providerIndex, consumer, derived} {
				temporaryLiveProviderRun(s, referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{{id}}, id))
			}
			for _, definition := range []struct {
				id, target int64
				expression string
			}{{defaultID, provider, "('context_provider'::regclass IS NOT NULL)"}, {generation, providerIndex, "('context_provider_index'::regclass IS NOT NULL)"}} {
				temporaryLiveProviderRun(s, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1 AND refobjid=$2", [][]any{pgDependEdge(2604, definition.id, 0, 1259, definition.target, 0, "n")}, definition.id, definition.target))
				temporaryLiveProviderRun(s, referenceSelect("SELECT pg_get_expr(adbin,adrelid) AS definition FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"definition"}, []string{"TEXT"}, [][]any{{definition.expression}}, definition.id))
			}
			temporaryLiveProviderRun(s, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2606 AND objid=$1 AND refobjid=$2", [][]any{pgDependEdge(2606, check, 0, 1259, providerIndex, 0, "n")}, check, providerIndex))
			temporaryLiveProviderRun(s, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1 AND refobjid=$2", [][]any{pgDependEdge(1259, derived, 0, 1259, provider, 0, "n")}, derived, provider))
			temporaryLiveProviderRun(s, referenceSelect("SELECT pg_get_indexdef($1,1,false) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"(('context_provider'::regclass IS NOT NULL))"}}, derived))
		}
		rows := func(values ...[2]int64) {
			want := make([][]any, 0, len(values))
			for _, value := range values {
				want = append(want, []any{value[0], true, true, value[1]})
			}
			temporaryLiveProviderRun(s, referenceSelect("SELECT * FROM context_consumer ORDER BY a", []string{"a", "d", "g", "z"}, []string{"INT4", "BOOL", "BOOL", "INT4"}, want))
		}
		catalog()
		rows([2]int64{7, 9})
		dump := temporaryLiveProviderDump(s, "context_provider", "context_consumer", "context_derived")
		if !s.postgres {
			if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
				s.t.Fatal(err)
			}
		}
		s.utility("CREATE TABLE public.context_after_reset(v INT)", nil)
		catalog()
		rows([2]int64{7, 9})
		s.exec(s.c, "INSERT INTO context_consumer(a,z) VALUES(11,13)", 1)
		if !s.postgres {
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump)); err != nil {
				s.t.Fatal(err)
			}
		}
		s.utility("CREATE TABLE public.context_after_restore(v INT)", nil)
		catalog()
		rows([2]int64{7, 9}, [2]int64{11, 13})
		// Rendering/replacing an unrelated default keeps every original provider
		// manifest; replacing d is deliberately last, after exact old-ID assertions.
		s.utility("ALTER TABLE context_consumer ALTER COLUMN z SET DEFAULT 17", nil)
		s.exec(s.c, "INSERT INTO context_consumer(a) VALUES(15)", 1)
		catalog()
		rows([2]int64{7, 9}, [2]int64{11, 13}, [2]int64{15, 17})
		s.exec(s.c, "UPDATE context_consumer SET z=19 WHERE a=7", 1)
		temporaryLiveProviderRun(s, referenceSelect("SELECT a,z FROM context_consumer WHERE 'context_provider'::regclass IS NOT NULL ORDER BY a", []string{"a", "z"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(19)}, {int64(11), int64(13)}, {int64(15), int64(17)}}))
		temporaryLiveProviderDump(s, "context_provider", "context_consumer", "context_derived")
	})
}

func TestReferenceTemporaryTableLiveProviderGeneratedProjection(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.exec(s.c, "CREATE TABLE public.projection_kept(v INT)", 0)
		s.exec(s.c, "INSERT INTO public.projection_kept VALUES(71)", 1)
		s.utility("CREATE TEMP TABLE projection_provider(v INT)", nil)
		s.exec(s.c, "CREATE INDEX projection_provider_index ON projection_provider(v)", 0)
		s.utility("CREATE TEMP TABLE projection_consumer(a INT,g BOOL GENERATED ALWAYS AS('projection_provider'::regclass IS NOT NULL) STORED,h BOOL GENERATED ALWAYS AS('projection_provider_index'::regclass IS NOT NULL) STORED,z INT,k INT GENERATED ALWAYS AS(z+1) STORED)", nil)
		s.exec(s.c, "INSERT INTO projection_consumer(a,z) VALUES(7,9)", 1)
		provider := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "projection_provider")
		index := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "projection_provider_index")
		consumer := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "projection_consumer")
		generation := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=5", consumer)
		g := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", consumer)
		h := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=3", consumer)
		catalog := func() {
			temporaryLiveProviderRun(s, referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{{consumer}}, consumer))
			temporaryLiveProviderRun(s, referenceSelect("SELECT oid FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{{generation}}, generation))
			temporaryLiveProviderRun(s, referenceSelect("SELECT pg_get_expr(adbin,adrelid) AS definition FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"definition"}, []string{"TEXT"}, [][]any{{"(z + 1)"}}, generation))
			temporaryLiveProviderRun(s, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", [][]any{pgDependEdge(2604, generation, 0, 1259, consumer, 5, "i"), pgDependEdge(2604, generation, 0, 1259, consumer, 4, "n")}, generation))
		}
		catalog()
		temporaryLiveProviderRun(s, referenceSelect("SELECT * FROM projection_consumer ORDER BY a", []string{"a", "g", "h", "z", "k"}, []string{"INT4", "BOOL", "BOOL", "INT4", "INT4"}, [][]any{{int64(7), true, true, int64(9), int64(10)}}))
		held := s.held(s.c, "INSERT INTO projection_consumer(a,z) VALUES($1,$2)")
		dump := temporaryLiveProviderDump(s, "projection_provider", "projection_consumer")
		if !s.postgres {
			if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
				s.t.Fatal(err)
			}
		}
		s.utility("CREATE TABLE public.after_projection_reset(v INT)", nil)
		catalog()
		if !s.postgres {
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump)); err != nil {
				s.t.Fatal(err)
			}
		}
		s.utility("CREATE TABLE public.after_projection_restore(v INT)", nil)
		catalog()
		// Both external generated columns disappear; the independent own-row
		// generation keeps its stable attribute/OID and remaps to the dense row.
		s.utility("DROP TABLE projection_provider CASCADE", nil)
		catalog()
		for _, id := range []int64{provider, index} {
			temporaryLiveProviderRun(s, referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{}, id))
		}
		for _, id := range []int64{g, h} {
			temporaryLiveProviderRun(s, referenceSelect("SELECT oid FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{}, id))
		}
		rows := func(values ...[3]int64) {
			expected := make([][]any, 0, len(values))
			for _, v := range values {
				expected = append(expected, []any{v[0], v[1], v[2]})
			}
			temporaryLiveProviderRun(s, referenceSelect("SELECT * FROM projection_consumer ORDER BY a", []string{"a", "z", "k"}, []string{"INT4", "INT4", "INT4"}, expected))
		}
		rows([3]int64{7, 9, 10})
		referenceRunStep(s.t, s.p.ctx, held, s.record, referenceExec(held.query, 1, 11, 13))
		s.exec(s.c, "UPDATE projection_consumer SET z=19 WHERE a=7", 1)
		rows([3]int64{7, 19, 20}, [3]int64{11, 13, 14})
		s.utility("CREATE TEMP TABLE projection_provider(v INT)", nil)
		s.exec(s.c, "CREATE INDEX projection_provider_index ON projection_provider(v)", 0)
		if temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "projection_provider") == provider || temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "projection_provider_index") == index {
			s.t.Fatal("recreated provider reused retired identity")
		}
		s.exec(s.c, "INSERT INTO projection_consumer(a,z) VALUES(21,23)", 1)
		rows([3]int64{7, 19, 20}, [3]int64{11, 13, 14}, [3]int64{21, 23, 24})
		catalog()
		temporaryLiveProviderDump(s, "projection_provider", "projection_consumer")
		temporaryTableRows(s, s.c, "public.projection_kept", 71)
	})
}

func TestReferenceTemporaryTablePredicateOnlyControls(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.exec(s.c, "CREATE TABLE public.predicate_kept(v INT)", 0)
		s.exec(s.c, "INSERT INTO public.predicate_kept VALUES(71)", 1)
		// Predicate result BOOL has no other TEMP column/default/CHECK/generation
		// or derived key that could accidentally retain its concrete type.
		s.utility("CREATE TEMP TABLE retained_partial(v INT)", nil)
		s.exec(s.c, "CREATE INDEX retained_partial_index ON retained_partial(v) WHERE v>0", 0)
		s.exec(s.c, "INSERT INTO retained_partial VALUES(7),(-3)", 2)
		table := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "retained_partial")
		index := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "retained_partial_index")
		catalog := func() {
			for _, id := range []int64{table, index} {
				temporaryLiveProviderRun(s, referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{{id}}, id))
			}
			temporaryLiveProviderRun(s, referenceSelect("SELECT pg_get_indexdef($1,0,false) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX retained_partial_index ON pg_temp.retained_partial USING btree (v) WHERE (v > 0)"}}, index))
			temporaryLiveProviderRun(s, referenceSelect("SELECT pg_get_indexdef($1,1,false) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"v"}}, index))
		}
		rows := func(all, positive, negative []int64) {
			for _, p := range []struct {
				q      string
				values []int64
			}{
				{"SELECT v FROM retained_partial ORDER BY v", all},
				{"SELECT v FROM retained_partial WHERE v>0 ORDER BY v", positive},
				{"SELECT v FROM retained_partial WHERE v<=0 ORDER BY v", negative},
			} {
				expected := make([][]any, 0, len(p.values))
				for _, v := range p.values {
					expected = append(expected, []any{v})
				}
				temporaryLiveProviderRun(s, referenceSelect(p.q, []string{"v"}, []string{"INT4"}, expected))
			}
		}
		catalog()
		rows([]int64{-3, 7}, []int64{7}, []int64{-3})
		held := s.held(s.c, "INSERT INTO retained_partial VALUES($1)")
		dump := temporaryLiveProviderDump(s, "retained_partial")
		if !s.postgres {
			if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
				s.t.Fatal(err)
			}
		}
		catalog()
		rows([]int64{-3, 7}, []int64{7}, []int64{-3})
		referenceRunStep(s.t, s.p.ctx, held, s.record, referenceExec(held.query, 1, -11))
		referenceRunStep(s.t, s.p.ctx, held, s.record, referenceExec(held.query, 1, 13))
		rows([]int64{-11, -3, 7, 13}, []int64{7, 13}, []int64{-11, -3})
		s.exec(s.c, "UPDATE retained_partial SET v=-7 WHERE v=7", 1)
		s.exec(s.c, "UPDATE retained_partial SET v=17 WHERE v=-3", 1)
		catalog()
		rows([]int64{-11, -7, 13, 17}, []int64{13, 17}, []int64{-11, -7})
		if !s.postgres {
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump)); err != nil {
				s.t.Fatal(err)
			}
		}
		catalog()
		rows([]int64{-11, -7, 13, 17}, []int64{13, 17}, []int64{-11, -7})
		referenceRunStep(s.t, s.p.ctx, held, s.record, referenceExec(held.query, 1, 19))
		rows([]int64{-11, -7, 13, 17, 19}, []int64{13, 17, 19}, []int64{-11, -7})
		temporaryLiveProviderDump(s, "retained_partial")
		temporaryTableRows(s, s.c, "public.predicate_kept", 71)
	})
}
