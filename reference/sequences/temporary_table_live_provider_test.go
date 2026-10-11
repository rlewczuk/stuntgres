package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func temporaryLiveProviderID(s *fixture.TempReferenceSession, q string, args ...any) int64 {
	s.T.Helper()
	var id int64
	if err := s.C.QueryRowContext(s.P.Ctx, q, args...).Scan(&id); err != nil || id <= 0 || id > 4294967295 {
		s.T.Fatal(q, args, id, err)
	}
	return id
}

func temporaryLiveProviderRun(s *fixture.TempReferenceSession, step fixture.Step) {
	step.Prepared = s.Prepared
	fixture.PgDescriptionExecutorRows(s.T, s.P, fixture.Backend{DB: s.DB, PG: s.Postgres, Recorder: s.Record}, s.C, step)
}

func temporaryLiveProviderDump(s *fixture.TempReferenceSession, names ...string) []byte {
	s.T.Helper()
	if s.Postgres {
		return nil
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
		s.T.Fatal(err)
	}
	for _, name := range append(names, "pg_temp_") {
		if strings.Contains(dump.String(), name) {
			s.T.Fatal("live TEMP leaked into persistent dump", name, dump.String())
		}
	}
	return dump.Bytes()
}

func TestReferenceTemporaryTableLiveProviderControls(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Exec(s.C, "CREATE TABLE public.live_kept(v INT)", 0)
		s.Exec(s.C, "INSERT INTO public.live_kept VALUES(71)", 1)
		s.Utility("CREATE TEMP TABLE live_provider(v INT)", nil)
		s.Utility("CREATE TEMP TABLE live_consumer(a INT,g BOOL GENERATED ALWAYS AS('live_provider'::regclass IS NOT NULL) STORED,z INT)", nil)
		s.Exec(s.C, "INSERT INTO live_provider VALUES(31)", 1)
		s.Exec(s.C, "INSERT INTO live_consumer(a,z) VALUES(7,9)", 1)
		namespace := temporaryLiveProviderID(s, "SELECT relnamespace FROM pg_catalog.pg_class WHERE relname=$1", "live_provider")
		s.Exec(s.Peer, "CREATE TEMP TABLE live_provider(v INT)", 0)
		s.Exec(s.Peer, "CREATE TEMP TABLE live_consumer(a INT,g BOOL GENERATED ALWAYS AS('live_provider'::regclass IS NOT NULL) STORED,z INT)", 0)
		s.Exec(s.Peer, "INSERT INTO live_provider VALUES(81)", 1)
		s.Exec(s.Peer, "INSERT INTO live_consumer(a,z) VALUES(81,82)", 1)
		provider := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1 AND relnamespace=$2", "live_provider", namespace)
		consumer := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1 AND relnamespace=$2", "live_consumer", namespace)
		generation := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", consumer)
		catalog := func() {
			temporaryLiveProviderRun(s, fixture.Select("SELECT oid FROM pg_catalog.pg_class WHERE relname=$1 AND relnamespace=$2", []string{"oid"}, []string{"OID"}, [][]any{{provider}}, "live_provider", namespace))
			temporaryLiveProviderRun(s, fixture.Select("SELECT oid FROM pg_catalog.pg_class WHERE relname=$1 AND relnamespace=$2", []string{"oid"}, []string{"OID"}, [][]any{{consumer}}, "live_consumer", namespace))
			temporaryLiveProviderRun(s, fixture.Select("SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", []string{"oid"}, []string{"OID"}, [][]any{{generation}}, consumer))
			temporaryLiveProviderRun(s, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", [][]any{fixture.PgDependEdge(2604, generation, 0, 1259, consumer, 2, "i"), fixture.PgDependEdge(2604, generation, 0, 1259, provider, 0, "n")}, generation))
			temporaryLiveProviderRun(s, fixture.Select("SELECT pg_get_expr(adbin,adrelid) AS definition FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"definition"}, []string{"TEXT"}, [][]any{{"('live_provider'::regclass IS NOT NULL)"}}, generation))
		}
		rows := func(pairs ...[2]int64) {
			temporaryExpressionRows(s, s.C, "live_consumer", true, pairs...)
			temporaryExpressionRows(s, s.Peer, "live_consumer", true, [2]int64{81, 82})
			fixture.TemporaryTableRows(s, s.C, "live_provider", 31)
			fixture.TemporaryTableRows(s, s.Peer, "live_provider", 81)
		}
		catalog()
		rows([2]int64{7, 9})
		held := s.Held(s.C, "INSERT INTO live_consumer(a,z) VALUES($1,$2)")
		dump := temporaryLiveProviderDump(s, "live_provider", "live_consumer")
		if !s.Postgres {
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
		}
		// Minimal generated-only consumer reaches the global generation scan during
		// unrelated persistent DDL, with its provider still live and unchanged.
		s.Utility("CREATE TABLE public.after_live_reset(v INT)", nil)
		catalog()
		rows([2]int64{7, 9})
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(held.SQL, 1, 11, 13))
		s.Utility("ALTER TABLE live_consumer ALTER COLUMN z SET DEFAULT 17", nil)
		s.Exec(s.C, "INSERT INTO live_consumer(a) VALUES(15)", 1)
		catalog()
		rows([2]int64{7, 9}, [2]int64{11, 13}, [2]int64{15, 17})
		if !s.Postgres {
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump)); err != nil {
				s.T.Fatal(err)
			}
		}
		s.Utility("CREATE TABLE public.after_live_restore(v INT)", nil)
		catalog()
		rows([2]int64{7, 9}, [2]int64{11, 13}, [2]int64{15, 17})
		s.Exec(s.C, "UPDATE live_consumer SET z=19 WHERE a=7", 1)
		s.Utility("ALTER TABLE live_consumer ALTER COLUMN z SET DEFAULT 23", nil)
		s.Exec(s.C, "INSERT INTO live_consumer(a) VALUES(21)", 1)
		rows([2]int64{7, 19}, [2]int64{11, 13}, [2]int64{15, 17}, [2]int64{21, 23})
		catalog()
		temporaryLiveProviderDump(s, "live_provider", "live_consumer")
		fixture.TemporaryTableRows(s, s.C, "public.live_kept", 71)
	})
}

func TestReferenceTemporaryTableLiveRelationProviderContexts(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Utility("CREATE TEMP TABLE context_provider(v INT)", nil)
		s.Exec(s.C, "CREATE INDEX context_provider_index ON context_provider(v)", 0)
		s.Utility("CREATE TEMP TABLE context_consumer(a INT,d BOOL DEFAULT ('context_provider'::regclass IS NOT NULL),g BOOL GENERATED ALWAYS AS('context_provider_index'::regclass IS NOT NULL) STORED,z INT,CONSTRAINT context_check CHECK('context_provider_index'::regclass IS NOT NULL))", nil)
		s.Exec(s.C, "CREATE INDEX context_derived ON context_consumer(('context_provider'::regclass IS NOT NULL))", 0)
		s.Exec(s.C, "INSERT INTO context_consumer(a,z) VALUES(7,9)", 1)
		provider := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname='context_provider'")
		providerIndex := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname='context_provider_index'")
		consumer := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname='context_consumer'")
		derived := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname='context_derived'")
		defaultID := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", consumer)
		generation := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=3", consumer)
		check := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='context_check'", consumer)
		catalog := func() {
			for _, id := range []int64{provider, providerIndex, consumer, derived} {
				temporaryLiveProviderRun(s, fixture.Select("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{{id}}, id))
			}
			for _, definition := range []struct {
				id, target int64
				expression string
			}{{defaultID, provider, "('context_provider'::regclass IS NOT NULL)"}, {generation, providerIndex, "('context_provider_index'::regclass IS NOT NULL)"}} {
				temporaryLiveProviderRun(s, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1 AND refobjid=$2", [][]any{fixture.PgDependEdge(2604, definition.id, 0, 1259, definition.target, 0, "n")}, definition.id, definition.target))
				temporaryLiveProviderRun(s, fixture.Select("SELECT pg_get_expr(adbin,adrelid) AS definition FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"definition"}, []string{"TEXT"}, [][]any{{definition.expression}}, definition.id))
			}
			temporaryLiveProviderRun(s, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2606 AND objid=$1 AND refobjid=$2", [][]any{fixture.PgDependEdge(2606, check, 0, 1259, providerIndex, 0, "n")}, check, providerIndex))
			temporaryLiveProviderRun(s, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=1259 AND objid=$1 AND refobjid=$2", [][]any{fixture.PgDependEdge(1259, derived, 0, 1259, provider, 0, "n")}, derived, provider))
			temporaryLiveProviderRun(s, fixture.Select("SELECT pg_get_indexdef($1,1,false) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"(('context_provider'::regclass IS NOT NULL))"}}, derived))
		}
		rows := func(values ...[2]int64) {
			want := make([][]any, 0, len(values))
			for _, value := range values {
				want = append(want, []any{value[0], true, true, value[1]})
			}
			temporaryLiveProviderRun(s, fixture.Select("SELECT * FROM context_consumer ORDER BY a", []string{"a", "d", "g", "z"}, []string{"INT4", "BOOL", "BOOL", "INT4"}, want))
		}
		catalog()
		rows([2]int64{7, 9})
		dump := temporaryLiveProviderDump(s, "context_provider", "context_consumer", "context_derived")
		if !s.Postgres {
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
		}
		s.Utility("CREATE TABLE public.context_after_reset(v INT)", nil)
		catalog()
		rows([2]int64{7, 9})
		s.Exec(s.C, "INSERT INTO context_consumer(a,z) VALUES(11,13)", 1)
		if !s.Postgres {
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump)); err != nil {
				s.T.Fatal(err)
			}
		}
		s.Utility("CREATE TABLE public.context_after_restore(v INT)", nil)
		catalog()
		rows([2]int64{7, 9}, [2]int64{11, 13})
		// Rendering/replacing an unrelated default keeps every original provider
		// manifest; replacing d is deliberately last, after exact old-ID assertions.
		s.Utility("ALTER TABLE context_consumer ALTER COLUMN z SET DEFAULT 17", nil)
		s.Exec(s.C, "INSERT INTO context_consumer(a) VALUES(15)", 1)
		catalog()
		rows([2]int64{7, 9}, [2]int64{11, 13}, [2]int64{15, 17})
		s.Exec(s.C, "UPDATE context_consumer SET z=19 WHERE a=7", 1)
		temporaryLiveProviderRun(s, fixture.Select("SELECT a,z FROM context_consumer WHERE 'context_provider'::regclass IS NOT NULL ORDER BY a", []string{"a", "z"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(19)}, {int64(11), int64(13)}, {int64(15), int64(17)}}))
		temporaryLiveProviderDump(s, "context_provider", "context_consumer", "context_derived")
	})
}

func TestReferenceTemporaryTableLiveProviderGeneratedProjection(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Exec(s.C, "CREATE TABLE public.projection_kept(v INT)", 0)
		s.Exec(s.C, "INSERT INTO public.projection_kept VALUES(71)", 1)
		s.Utility("CREATE TEMP TABLE projection_provider(v INT)", nil)
		s.Exec(s.C, "CREATE INDEX projection_provider_index ON projection_provider(v)", 0)
		s.Utility("CREATE TEMP TABLE projection_consumer(a INT,g BOOL GENERATED ALWAYS AS('projection_provider'::regclass IS NOT NULL) STORED,h BOOL GENERATED ALWAYS AS('projection_provider_index'::regclass IS NOT NULL) STORED,z INT,k INT GENERATED ALWAYS AS(z+1) STORED)", nil)
		s.Exec(s.C, "INSERT INTO projection_consumer(a,z) VALUES(7,9)", 1)
		provider := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "projection_provider")
		index := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "projection_provider_index")
		consumer := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "projection_consumer")
		generation := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=5", consumer)
		g := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", consumer)
		h := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=3", consumer)
		catalog := func() {
			temporaryLiveProviderRun(s, fixture.Select("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{{consumer}}, consumer))
			temporaryLiveProviderRun(s, fixture.Select("SELECT oid FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{{generation}}, generation))
			temporaryLiveProviderRun(s, fixture.Select("SELECT pg_get_expr(adbin,adrelid) AS definition FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"definition"}, []string{"TEXT"}, [][]any{{"(z + 1)"}}, generation))
			temporaryLiveProviderRun(s, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", [][]any{fixture.PgDependEdge(2604, generation, 0, 1259, consumer, 5, "i"), fixture.PgDependEdge(2604, generation, 0, 1259, consumer, 4, "n")}, generation))
		}
		catalog()
		temporaryLiveProviderRun(s, fixture.Select("SELECT * FROM projection_consumer ORDER BY a", []string{"a", "g", "h", "z", "k"}, []string{"INT4", "BOOL", "BOOL", "INT4", "INT4"}, [][]any{{int64(7), true, true, int64(9), int64(10)}}))
		held := s.Held(s.C, "INSERT INTO projection_consumer(a,z) VALUES($1,$2)")
		dump := temporaryLiveProviderDump(s, "projection_provider", "projection_consumer")
		if !s.Postgres {
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
		}
		s.Utility("CREATE TABLE public.after_projection_reset(v INT)", nil)
		catalog()
		if !s.Postgres {
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump)); err != nil {
				s.T.Fatal(err)
			}
		}
		s.Utility("CREATE TABLE public.after_projection_restore(v INT)", nil)
		catalog()
		// Both external generated columns disappear; the independent own-row
		// generation keeps its stable attribute/OID and remaps to the dense row.
		s.Utility("DROP TABLE projection_provider CASCADE", nil)
		catalog()
		for _, id := range []int64{provider, index} {
			temporaryLiveProviderRun(s, fixture.Select("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{}, id))
		}
		for _, id := range []int64{g, h} {
			temporaryLiveProviderRun(s, fixture.Select("SELECT oid FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{}, id))
		}
		rows := func(values ...[3]int64) {
			expected := make([][]any, 0, len(values))
			for _, v := range values {
				expected = append(expected, []any{v[0], v[1], v[2]})
			}
			temporaryLiveProviderRun(s, fixture.Select("SELECT * FROM projection_consumer ORDER BY a", []string{"a", "z", "k"}, []string{"INT4", "INT4", "INT4"}, expected))
		}
		rows([3]int64{7, 9, 10})
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(held.SQL, 1, 11, 13))
		s.Exec(s.C, "UPDATE projection_consumer SET z=19 WHERE a=7", 1)
		rows([3]int64{7, 19, 20}, [3]int64{11, 13, 14})
		s.Utility("CREATE TEMP TABLE projection_provider(v INT)", nil)
		s.Exec(s.C, "CREATE INDEX projection_provider_index ON projection_provider(v)", 0)
		if temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "projection_provider") == provider || temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "projection_provider_index") == index {
			s.T.Fatal("recreated provider reused retired identity")
		}
		s.Exec(s.C, "INSERT INTO projection_consumer(a,z) VALUES(21,23)", 1)
		rows([3]int64{7, 19, 20}, [3]int64{11, 13, 14}, [3]int64{21, 23, 24})
		catalog()
		temporaryLiveProviderDump(s, "projection_provider", "projection_consumer")
		fixture.TemporaryTableRows(s, s.C, "public.projection_kept", 71)
	})
}

func TestReferenceTemporaryTablePredicateOnlyControls(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Exec(s.C, "CREATE TABLE public.predicate_kept(v INT)", 0)
		s.Exec(s.C, "INSERT INTO public.predicate_kept VALUES(71)", 1)
		// Predicate result BOOL has no other TEMP column/default/CHECK/generation
		// or derived key that could accidentally retain its concrete type.
		s.Utility("CREATE TEMP TABLE retained_partial(v INT)", nil)
		s.Exec(s.C, "CREATE INDEX retained_partial_index ON retained_partial(v) WHERE v>0", 0)
		s.Exec(s.C, "INSERT INTO retained_partial VALUES(7),(-3)", 2)
		table := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "retained_partial")
		index := temporaryLiveProviderID(s, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", "retained_partial_index")
		catalog := func() {
			for _, id := range []int64{table, index} {
				temporaryLiveProviderRun(s, fixture.Select("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{{id}}, id))
			}
			temporaryLiveProviderRun(s, fixture.Select("SELECT pg_get_indexdef($1,0,false) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX retained_partial_index ON pg_temp.retained_partial USING btree (v) WHERE (v > 0)"}}, index))
			temporaryLiveProviderRun(s, fixture.Select("SELECT pg_get_indexdef($1,1,false) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"v"}}, index))
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
				temporaryLiveProviderRun(s, fixture.Select(p.q, []string{"v"}, []string{"INT4"}, expected))
			}
		}
		catalog()
		rows([]int64{-3, 7}, []int64{7}, []int64{-3})
		held := s.Held(s.C, "INSERT INTO retained_partial VALUES($1)")
		dump := temporaryLiveProviderDump(s, "retained_partial")
		if !s.Postgres {
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
		}
		catalog()
		rows([]int64{-3, 7}, []int64{7}, []int64{-3})
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(held.SQL, 1, -11))
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(held.SQL, 1, 13))
		rows([]int64{-11, -3, 7, 13}, []int64{7, 13}, []int64{-11, -3})
		s.Exec(s.C, "UPDATE retained_partial SET v=-7 WHERE v=7", 1)
		s.Exec(s.C, "UPDATE retained_partial SET v=17 WHERE v=-3", 1)
		catalog()
		rows([]int64{-11, -7, 13, 17}, []int64{13, 17}, []int64{-11, -7})
		if !s.Postgres {
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump)); err != nil {
				s.T.Fatal(err)
			}
		}
		catalog()
		rows([]int64{-11, -7, 13, 17}, []int64{13, 17}, []int64{-11, -7})
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(held.SQL, 1, 19))
		rows([]int64{-11, -7, 13, 17, 19}, []int64{13, 17, 19}, []int64{-11, -7})
		temporaryLiveProviderDump(s, "retained_partial")
		fixture.TemporaryTableRows(s, s.C, "public.predicate_kept", 71)
	})
}
