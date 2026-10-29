package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func ddlCommandsLikeIndexes(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		d.exec("CREATE TABLE et_dc_like_source(i integer)", "CREATE INDEX et_dc_source_idx ON et_dc_like_source(i)")
		d.run(fixture.Exec("INSERT INTO et_dc_like_source VALUES(7)", 1))
		d.install("'CREATE TABLE'")
		for _, prepared := range []bool{false, true} {
			d.exec("BEGIN")
			s := fixture.Exec("CREATE TABLE et_dc_like_copy(LIKE et_dc_like_source INCLUDING INDEXES)", 0)
			s.Prepared = prepared
			d.run(s)
			// PostgreSQL chooses the new relation/column name, not the source index name.
			want := append(ddlCommandRows("1259", "CREATE TABLE", "table", "public", "public.et_dc_like_copy"), ddlCommandRows("1259", "CREATE INDEX", "index", "public", "public.et_dc_like_copy_i_idx")...)
			d.rows(want)
			d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_class c ON a.objid=c.oid WHERE a.classid='pg_class'::regclass AND a.command_tag='CREATE TABLE' AND c.oid='et_dc_like_copy'::regclass AND c.relkind='r'", 2))
			d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_index i ON a.objid=i.indexrelid JOIN pg_class c ON c.oid=i.indexrelid WHERE a.classid='pg_class'::regclass AND a.command_tag='CREATE INDEX' AND i.indrelid='et_dc_like_copy'::regclass AND c.relname='et_dc_like_copy_i_idx' AND c.relkind='i'", 2))
			index := fixture.Select("SELECT c.relname,i.indisunique,i.indisprimary,i.indisvalid,i.indisready FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='et_dc_like_copy'::regclass", []string{"relname", "indisunique", "indisprimary", "indisvalid", "indisready"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"et_dc_like_copy_i_idx", false, false, true, true}})
			d.run(index)
			d.run(fixture.Prepared(index))
			data := fixture.Select("SELECT i FROM et_dc_like_copy", []string{"i"}, []string{"INT4"}, nil)
			d.run(data)
			d.run(fixture.Prepared(data))
			d.run(fixture.Exec("INSERT INTO et_dc_like_copy VALUES(8),(8)", 2))
			data = fixture.Select("SELECT i FROM et_dc_like_copy ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(8)}})
			d.run(data)
			d.run(fixture.Prepared(data))
			data = fixture.Select("SELECT i FROM et_dc_like_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}})
			d.run(data)
			d.run(fixture.Prepared(data))
			d.exec("ROLLBACK")
		}
	})
}

// Preserve the existing admission boundary rather than making recursive index
// execution a prerequisite of command inspection. Both routes retain rollback,
// exact receipts (or their absence) and catalog atomicity.
func ddlCommandsPartitionIndexBoundary(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		d.exec("CREATE TABLE et_dc_boundary_parent(i integer) PARTITION BY RANGE(i)",
			"CREATE TABLE et_dc_boundary_child PARTITION OF et_dc_boundary_parent FOR VALUES FROM(0) TO(10)")
		d.run(fixture.Exec("INSERT INTO et_dc_boundary_parent VALUES(7)", 1))
		d.install("'CREATE INDEX'")
		for _, prepared := range []bool{false, true} {
			d.exec("BEGIN")
			query := "CREATE INDEX et_dc_boundary_idx ON et_dc_boundary_parent(i)"
			s := fixture.Exec(query, 0)
			if !d.b.PG {
				s = fixture.ErrorDiagnostic(query, eventTriggerDiagnostic("0A000", "partitioned indexes are unsupported"))
				s.ErrorStage = "execute"
				d.exec("SAVEPOINT et_dc_boundary")
			}
			s.Prepared = prepared
			d.run(s)
			if d.b.PG {
				d.rows(ddlCommandRows("1259", "CREATE INDEX", "index", "public", "public.et_dc_boundary_idx"))
				d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_class c ON a.objid=c.oid WHERE c.relname='et_dc_boundary_idx' AND c.relkind='I' AND a.classid='pg_class'::regclass", 2))
			} else {
				d.exec("ROLLBACK TO SAVEPOINT et_dc_boundary")
				d.rows(nil)
				d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_dc_boundary_idx'", 0))
				d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_index WHERE indrelid IN ('et_dc_boundary_parent'::regclass,'et_dc_boundary_child'::regclass)", 0))
			}
			d.run(fixture.Select("SELECT i FROM et_dc_boundary_parent", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			d.exec("ROLLBACK")
			d.rows(nil)
			d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_index WHERE indrelid IN ('et_dc_boundary_parent'::regclass,'et_dc_boundary_child'::regclass)", 0))
		}
	})
}
