package reference_test

import "testing"

type temporaryCompositionObjects struct {
	owner, sequence, survivor, g, h, index, partial int64
}

func temporaryCompositionSetup(s *tempReferenceSession) temporaryCompositionObjects {
	s.t.Helper()
	s.utility("CREATE TEMP TABLE composition_owner(id SERIAL)", nil)
	s.utility("CREATE TEMP TABLE composition_survivor(a INT,g BOOL GENERATED ALWAYS AS('composition_owner'::regclass IS NOT NULL) STORED,h BOOL GENERATED ALWAYS AS('composition_owner_id_seq'::regclass IS NOT NULL) STORED,z INT)", nil)
	s.exec(s.c, "CREATE INDEX composition_index ON composition_survivor((z+0))", 0)
	s.exec(s.c, "CREATE INDEX composition_partial_index ON composition_survivor((z+0)) WHERE z>0", 0)
	s.exec(s.c, "INSERT INTO composition_survivor(a,z) VALUES(7,9)", 1)
	var o temporaryCompositionObjects
	for _, p := range []struct {
		q    string
		args []any
		id   *int64
	}{
		{"SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", []any{"composition_owner"}, &o.owner},
		{"SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", []any{"composition_owner_id_seq"}, &o.sequence},
		{"SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", []any{"composition_survivor"}, &o.survivor},
		{"SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", []any{"composition_index"}, &o.index},
		{"SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", []any{"composition_partial_index"}, &o.partial},
	} {
		if err := s.c.QueryRowContext(s.p.ctx, p.q, p.args...).Scan(p.id); err != nil || *p.id <= 0 || *p.id > 4294967295 {
			s.t.Fatal(p.q, p.args, *p.id, err)
		}
	}
	for _, p := range []struct {
		attribute int
		id        *int64
	}{{2, &o.g}, {3, &o.h}} {
		q := "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=$2"
		if err := s.c.QueryRowContext(s.p.ctx, q, o.survivor, p.attribute).Scan(p.id); err != nil || *p.id <= 0 || *p.id > 4294967295 {
			s.t.Fatal(q, p.attribute, *p.id, err)
		}
	}
	temporaryCompositionCatalog(s, o, true)
	temporaryCompositionRows(s, true, [2]int64{7, 9})
	return o
}

func temporaryCompositionCatalog(s *tempReferenceSession, o temporaryCompositionObjects, present bool) {
	s.t.Helper()
	backend := referenceBackend{db: s.db, pg: s.postgres, recorder: s.record}
	run := func(step referenceStep) {
		step.prepared = s.prepared
		pgDescriptionExecutorRows(s.t, s.p, backend, s.c, step)
	}
	for _, p := range []struct{ id, provider, attribute int64 }{{o.g, o.owner, 2}, {o.h, o.sequence, 3}} {
		edges := [][]any{}
		definitions := [][]any{}
		if present {
			edges = [][]any{pgDependEdge(2604, p.id, 0, 1259, o.survivor, p.attribute, "i"), pgDependEdge(2604, p.id, 0, 1259, p.provider, 0, "n")}
			definitions = [][]any{{p.id}}
		}
		run(pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=2604 AND objid=$1", edges, p.id))
		run(referenceSelect("SELECT oid FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"oid"}, []string{"OID"}, definitions, p.id))
	}
	for _, id := range []int64{o.owner, o.sequence} {
		rows := [][]any{}
		if present {
			rows = [][]any{{id}}
		}
		run(referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, rows, id))
	}
	run(referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{{o.index}}, o.index))
	run(referenceSelect("SELECT hasindexes FROM pg_catalog.pg_tables WHERE tablename=$1", []string{"hasindexes"}, []string{"BOOL"}, [][]any{{true}}, "composition_survivor"))
	// Independently specified definition survives both dense-slot projections;
	// key expression output is namespace-independent, unlike full TEMP deparse.
	run(referenceSelect("SELECT pg_get_indexdef($1,1,false) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"((z + 0))"}}, o.index))
	run(referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{{o.partial}}, o.partial))
	run(referenceSelect("SELECT pg_get_indexdef($1,1,false) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"((z + 0))"}}, o.partial))
	// PostgreSQL deparses the own temporary namespace using its pg_temp alias.
	// Assert the complete definition, not an opaque formatting predicate.
	run(referenceSelect("SELECT pg_get_indexdef($1,0,false) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX composition_partial_index ON pg_temp.composition_survivor USING btree (((z + 0))) WHERE (z > 0)"}}, o.partial))
}

func temporaryCompositionRows(s *tempReferenceSession, generated bool, values ...[2]int64) {
	s.t.Helper()
	columns, types := []string{"a", "z"}, []string{"INT4", "INT4"}
	if generated {
		columns, types = []string{"a", "g", "h", "z"}, []string{"INT4", "BOOL", "BOOL", "INT4"}
	}
	rows := make([][]any, 0, len(values))
	for _, v := range values {
		if generated {
			rows = append(rows, []any{v[0], true, true, v[1]})
		} else {
			rows = append(rows, []any{v[0], v[1]})
		}
	}
	step := referenceSelect("SELECT * FROM composition_survivor ORDER BY a", columns, types, rows)
	step.prepared = s.prepared
	referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
}

func temporaryCompositionCurrentDML(s *tempReferenceSession) {
	s.exec(s.c, "INSERT INTO composition_survivor(a,z) VALUES(11,13)", 1)
	s.exec(s.c, "UPDATE composition_survivor SET z=19 WHERE a=7", 1)
	for _, p := range []struct {
		q    string
		rows [][]any
	}{
		{"SELECT a,z FROM composition_survivor WHERE z+0=13", [][]any{{int64(11), int64(13)}}},
		{"SELECT a,z FROM composition_survivor WHERE z+0=19", [][]any{{int64(7), int64(19)}}},
		{"SELECT a,z FROM composition_survivor WHERE z+0=9", [][]any{}},
	} {
		step := referenceSelect(p.q, []string{"a", "z"}, []string{"INT4", "INT4"}, p.rows)
		step.prepared = s.prepared
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
	}
	s.exec(s.c, "DELETE FROM composition_survivor WHERE a=11", 1)
	temporaryCompositionRows(s, false, [2]int64{7, 19})
	s.exec(s.c, "UPDATE composition_survivor SET z=-19 WHERE a=7", 1)
	for _, p := range []struct {
		q    string
		rows [][]any
	}{
		{"SELECT a,z FROM composition_survivor WHERE z>0 AND z+0=-19", [][]any{}},
		{"SELECT a,z FROM composition_survivor WHERE z<=0 AND z+0=-19", [][]any{{int64(7), int64(-19)}}},
		{"SELECT a,z FROM composition_survivor WHERE z>0 AND z+0=19", [][]any{}},
	} {
		step := referenceSelect(p.q, []string{"a", "z"}, []string{"INT4", "INT4"}, p.rows)
		step.prepared = s.prepared
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
	}
	temporaryCompositionRows(s, false, [2]int64{7, -19})
	s.exec(s.c, "UPDATE composition_survivor SET z=23 WHERE a=7", 1)
	for _, p := range []struct {
		q    string
		rows [][]any
	}{
		{"SELECT a,z FROM composition_survivor WHERE z>0 AND z+0=23", [][]any{{int64(7), int64(23)}}},
		{"SELECT a,z FROM composition_survivor WHERE z<=0 AND z+0=23", [][]any{}},
		{"SELECT a,z FROM composition_survivor WHERE z+0=-19", [][]any{}},
	} {
		step := referenceSelect(p.q, []string{"a", "z"}, []string{"INT4", "INT4"}, p.rows)
		step.prepared = s.prepared
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
	}
	temporaryCompositionRows(s, false, [2]int64{7, 23})
}

func TestReferenceTemporaryTableIndexCompositionImmediate(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		objects := temporaryCompositionSetup(s)
		s.utility("DROP TABLE composition_owner CASCADE", nil)
		temporaryCompositionCatalog(s, objects, false)
		temporaryCompositionRows(s, false, [2]int64{7, 9})
		temporaryCompositionCurrentDML(s)
		temporaryCompositionCatalog(s, objects, false)
	})
}

func TestReferenceTemporaryTableIndexCompositionTransactional(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		objects := temporaryCompositionSetup(s)
		s.exec(s.c, "BEGIN", 0)
		s.exec(s.c, "INSERT INTO composition_survivor(a,z) VALUES(11,13)", 1)
		s.exec(s.c, "SAVEPOINT before_composition", 0)
		s.utility("DROP TABLE composition_owner CASCADE", nil)
		temporaryCompositionCatalog(s, objects, false)
		temporaryCompositionRows(s, false, [2]int64{7, 9}, [2]int64{11, 13})
		s.exec(s.c, "ROLLBACK TO before_composition", 0)
		temporaryCompositionCatalog(s, objects, true)
		temporaryCompositionRows(s, true, [2]int64{7, 9}, [2]int64{11, 13})
		s.utility("DROP TABLE composition_owner CASCADE", nil)
		s.exec(s.c, "ROLLBACK", 0)
		temporaryCompositionCatalog(s, objects, true)
		temporaryCompositionRows(s, true, [2]int64{7, 9})
		s.exec(s.c, "BEGIN", 0)
		s.utility("DROP TABLE composition_owner CASCADE", nil)
		temporaryCompositionRows(s, false, [2]int64{7, 9})
		s.exec(s.c, "COMMIT", 0)
		temporaryCompositionCatalog(s, objects, false)
		temporaryCompositionRows(s, false, [2]int64{7, 9})
		temporaryCompositionCurrentDML(s)
	})
}
