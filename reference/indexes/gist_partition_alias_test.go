package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGiSTPartitionAccessMethodAlias(t *testing.T) {
	amBackends(t, []string{"gist_partition_alias"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec(`CREATE ACCESS METHOD gist_partition_alias TYPE INDEX HANDLER pg_catalog.gisthandler`, 0))
		// A method alias has its own operator classes: the canonical point_ops
		// family belongs to gist, so declare the required point support here.
		run(fixture.Exec(`CREATE OPERATOR CLASS public.gist_partition_alias_ops FOR TYPE point USING gist_partition_alias AS
			OPERATOR 6 pg_catalog.~=(point,point),
			FUNCTION 1 pg_catalog.gist_point_consistent(internal,point,smallint,oid,internal),
			FUNCTION 2 pg_catalog.gist_box_union(internal,internal),
			FUNCTION 3 pg_catalog.gist_point_compress(internal),
			FUNCTION 5 pg_catalog.gist_box_penalty(internal,internal,internal),
			FUNCTION 6 pg_catalog.gist_box_picksplit(internal,internal),
			FUNCTION 7 pg_catalog.gist_box_same(box,box,internal),
			STORAGE box`, 0))
		run(fixture.Exec(`CREATE TABLE gist_alias_parent(id integer,p point) PARTITION BY RANGE(id)`, 0))
		run(fixture.Exec(`CREATE TABLE gist_alias_leaf PARTITION OF gist_alias_parent FOR VALUES FROM(0) TO(10)`, 0))
		run(fixture.Exec(`INSERT INTO gist_alias_parent VALUES(1,'(0,0)'),(2,'(3,4)'),(3,NULL)`, 3))
		run(fixture.Prepared(fixture.Exec(`CREATE INDEX gist_alias_parent_i ON gist_alias_parent USING gist_partition_alias(p public.gist_partition_alias_ops) WITH(buffering=off,fillfactor=80)`, 0)))
		classRoutes(t, p, b, conn, fixture.Select(
			`SELECT i.indrelid='gist_alias_parent'::regclass AS parent,c.relam=m.oid AS alias_method,m.oid<>783 AS custom_identity,i.indclass[0]=o.oid AS alias_class,c.reloptions::text AS options,a.attoptions::text AS key_options FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=1 JOIN pg_am m ON m.amname='gist_partition_alias' JOIN pg_opclass o ON o.opcname='gist_partition_alias_ops' AND o.opcmethod=m.oid WHERE i.indrelid IN ('gist_alias_parent'::regclass,'gist_alias_leaf'::regclass) ORDER BY parent`,
			[]string{"parent", "alias_method", "custom_identity", "alias_class", "options", "key_options"},
			[]string{"BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT"},
			[][]any{{false, true, true, true, "{buffering=off,fillfactor=80}", nil}, {true, true, true, true, "{buffering=off,fillfactor=80}", nil}}))
		const query = `SELECT id FROM gist_alias_parent WHERE p ~= $1::point ORDER BY id`
		held := classHeld(t, p, conn, query)
		check := func(rows [][]any) {
			s := fixture.Select(query, []string{"id"}, []string{"INT4"}, rows, "(0,0)")
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		check([][]any{{int64(1)}})
		run(fixture.Exec(`INSERT INTO gist_alias_parent VALUES(4,'(0,0)')`, 1))
		run(fixture.Prepared(fixture.Exec(`UPDATE gist_alias_leaf SET p='(0,0)' WHERE id=2`, 1)))
		run(fixture.Exec(`UPDATE gist_alias_leaf SET p='(1,0)' WHERE id=1`, 1))
		check([][]any{{int64(2)}, {int64(4)}})
		classRoutes(t, p, b, conn, fixture.Select(`SELECT id,p::text AS p FROM gist_alias_parent ORDER BY id`, []string{"id", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "(1,0)"}, {int64(2), "(0,0)"}, {int64(3), nil}, {int64(4), "(0,0)"}}))
	})
}
