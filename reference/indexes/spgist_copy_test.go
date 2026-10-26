package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

const spgistPointMembers = `OPERATOR 6 pg_catalog.~=(point,point),
 OPERATOR 15 pg_catalog.<->(point,point) FOR ORDER BY pg_catalog.float_ops,
 FUNCTION 1 pg_catalog.spg_quad_config(internal,internal),
 FUNCTION 2 pg_catalog.spg_quad_choose(internal,internal),
 FUNCTION 3 pg_catalog.spg_quad_picksplit(internal,internal),
 FUNCTION 4 pg_catalog.spg_quad_inner_consistent(internal,internal),
 FUNCTION 5 pg_catalog.spg_quad_leaf_consistent(internal,internal)`

func TestReferenceSPGiSTCopies(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		for _, q := range []string{
			"CREATE TABLE spgist_copy(id integer,p point)",
			"CREATE INDEX spgist_copy_i ON spgist_copy USING spgist(p kd_point_ops) INCLUDE(id) WITH(fillfactor=80)",
			"CREATE TABLE spgist_like(LIKE spgist_copy INCLUDING INDEXES)",
			"CREATE TABLE spgist_parent(id integer,p point) PARTITION BY RANGE(id)",
			"CREATE TABLE spgist_leaf PARTITION OF spgist_parent FOR VALUES FROM(0) TO(10)",
			"CREATE INDEX spgist_parent_i ON spgist_parent USING spgist(p) WITH(fillfactor=80)",
			"CREATE TABLE spgist_later PARTITION OF spgist_parent FOR VALUES FROM(10) TO(20)",
			"CREATE TABLE spgist_attach(id integer,p point)",
			"ALTER TABLE spgist_parent ATTACH PARTITION spgist_attach FOR VALUES FROM(20) TO(30)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO spgist_copy VALUES(1,'(0,0)')", 1))
		run(fixture.Exec("INSERT INTO spgist_like VALUES(2,'(3,4)'),(3,NULL),(4,'(3,4)')", 3))
		run(fixture.Exec("INSERT INTO spgist_parent VALUES(1,'(0,0)'),(11,'(3,4)'),(21,NULL)", 3))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id,p <-> point '(0,0)' AS d FROM spgist_like ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(2), float64(5)}, {int64(3), nil}, {int64(4), float64(5)}}))
		run(fixture.Exec("UPDATE spgist_leaf SET p='(3,4)' WHERE id=1", 1))
		run(fixture.Exec("INSERT INTO spgist_parent VALUES(2,'(3,4)')", 1))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_parent WHERE p ~= point '(3,4)' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(11)}}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT c.opcname,r.reloptions::text AS options FROM pg_index i JOIN pg_class r ON r.oid=i.indexrelid JOIN pg_opclass c ON c.oid=i.indclass[0] WHERE i.indrelid='spgist_like'::regclass", []string{"opcname", "options"}, []string{"NAME", "TEXT"}, [][]any{{"kd_point_ops", "{fillfactor=80}"}}))
	})
	amBackends(t, []string{"spgist_alias"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE ACCESS METHOD spgist_alias TYPE INDEX HANDLER pg_catalog.spghandler", 0))
		run(fixture.Exec("CREATE OPERATOR CLASS spgist_alias_ops FOR TYPE point USING spgist_alias AS "+spgistPointMembers, 0))
		run(fixture.Exec("CREATE TABLE spgist_alias_parent(id integer,p point) PARTITION BY RANGE(id)", 0))
		run(fixture.Exec("CREATE TABLE spgist_alias_leaf PARTITION OF spgist_alias_parent FOR VALUES FROM(0) TO(10)", 0))
		run(fixture.Exec("CREATE INDEX spgist_alias_i ON spgist_alias_parent USING spgist_alias(p spgist_alias_ops) WITH(fillfactor=80)", 0))
		run(fixture.Exec("INSERT INTO spgist_alias_parent VALUES(1,'(0,0)'),(2,'(3,4)'),(3,NULL)", 3))
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_alias_parent WHERE p ~= point '(0,0)' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT m.amname,c.opcname,m.oid<>4000 AS distinct_method,c.opcfamily<>4015 AS distinct_family,pg_index_column_has_property(r.oid,1,'distance_orderable') AS distance FROM pg_index i JOIN pg_class r ON r.oid=i.indexrelid JOIN pg_am m ON m.oid=r.relam JOIN pg_opclass c ON c.oid=i.indclass[0] WHERE i.indrelid='spgist_alias_parent'::regclass", []string{"amname", "opcname", "distinct_method", "distinct_family", "distance"}, []string{"NAME", "NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{"spgist_alias", "spgist_alias_ops", true, true, true}}))
	})
}

func TestReferenceSPGiSTFamilyDefinitions(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE OPERATOR FAMILY spgist_family USING spgist", 0))
		run(fixture.Exec("CREATE OPERATOR CLASS spgist_user_point FOR TYPE point USING spgist FAMILY spgist_family AS "+spgistPointMembers, 0))
		run(fixture.Exec("CREATE TABLE spgist_user(p point)", 0))
		run(fixture.Exec("CREATE INDEX spgist_user_i ON spgist_user USING spgist(p spgist_user_point)", 0))
		run(fixture.Exec("INSERT INTO spgist_user VALUES('(3,4)'),(NULL)", 2))
		classRoutes(t, p, b, conn, fixture.Select("SELECT p <-> point '(0,0)' AS d FROM spgist_user ORDER BY d NULLS LAST", []string{"d"}, []string{"FLOAT8"}, [][]any{{float64(5)}, {nil}}))
		run(fixture.Exec("CREATE OPERATOR CLASS spgist_user_poly FOR TYPE polygon USING spgist AS OPERATOR 3 pg_catalog.&&(polygon,polygon),FUNCTION 1 pg_catalog.spg_bbox_quad_config(internal,internal),FUNCTION 2 pg_catalog.spg_box_quad_choose(internal,internal),FUNCTION 3 pg_catalog.spg_box_quad_picksplit(internal,internal),FUNCTION 4 pg_catalog.spg_box_quad_inner_consistent(internal,internal),FUNCTION 5 pg_catalog.spg_box_quad_leaf_consistent(internal,internal),FUNCTION 6 pg_catalog.spg_poly_quad_compress(polygon),STORAGE box", 0))
		run(fixture.Exec("CREATE TABLE spgist_user_polygon(p polygon)", 0))
		// SP-GiST's property callback recognizes either operand of a live
		// ordering member. Neither a sortable result alone nor a valid class
		// with an unrelated ordering operator establishes that capability.
		const boxSupports = `FUNCTION 1 pg_catalog.spg_box_quad_config(internal,internal),
 FUNCTION 2 pg_catalog.spg_box_quad_choose(internal,internal),
 FUNCTION 3 pg_catalog.spg_box_quad_picksplit(internal,internal),
 FUNCTION 4 pg_catalog.spg_box_quad_inner_consistent(internal,internal),
 FUNCTION 5 pg_catalog.spg_box_quad_leaf_consistent(internal,internal)`
		run(fixture.Exec("CREATE OPERATOR CLASS spgist_right_input FOR TYPE box USING spgist AS OPERATOR 15 pg_catalog.<->(point,box) FOR ORDER BY pg_catalog.float_ops,"+boxSupports, 0))
		run(fixture.Exec("CREATE OPERATOR CLASS spgist_neither_input FOR TYPE box USING spgist AS OPERATOR 15 pg_catalog.<->(point,point) FOR ORDER BY pg_catalog.float_ops,"+boxSupports, 0))
		run(fixture.Exec("CREATE TABLE spgist_member_inputs(b box)", 0))
		run(fixture.Exec("CREATE INDEX spgist_right_input_i ON spgist_member_inputs USING spgist(b spgist_right_input)", 0))
		run(fixture.Exec("CREATE INDEX spgist_neither_input_i ON spgist_member_inputs USING spgist(b spgist_neither_input)", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT relname,pg_index_column_has_property(oid,1,'distance_orderable') AS distance FROM pg_class WHERE relname IN ('spgist_right_input_i','spgist_neither_input_i') ORDER BY relname", []string{"relname", "distance"}, []string{"NAME", "BOOL"}, [][]any{{"spgist_neither_input_i", false}, {"spgist_right_input_i", true}}))
		run(fixture.Exec("CREATE INDEX ON spgist_user_polygon USING spgist(p spgist_user_poly)", 0))
		d := fixture.ControlsReferenceDiagnostic("42P17", "invalid function number 8, must be between 1 and 7")
		classRoutes(t, p, b, conn, fixture.ErrorDiagnostic("CREATE OPERATOR CLASS spgist_invalid_class FOR TYPE point USING spgist AS FUNCTION 8 pg_catalog.spg_quad_config(internal,internal)", d))
		run(fixture.Select("SELECT opcname FROM pg_opclass WHERE opcname='spgist_invalid_class'", []string{"opcname"}, []string{"NAME"}, nil))
	})
}
