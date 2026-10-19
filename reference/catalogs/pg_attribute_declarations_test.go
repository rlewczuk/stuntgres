package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGAttributeTypes(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB,
			"CREATE DOMAIN pa18_type_domain AS integer",
			"CREATE TYPE pa18_type_enum AS ENUM('one','two')",
			"CREATE TYPE pa18_type_composite AS(x integer)",
			"CREATE TABLE pa18_types(i integer,n name,t text,v varchar(7),c char(3),d numeric(8,2),u uuid,j interval,s timestamp(3),b bit(5),a integer[],f double precision[],acl aclitem,dom pa18_type_domain,e pa18_type_enum,co pa18_type_composite,r int4range,m int4multirange,bc char,bb bit)")
		id := pgAttributeID(t, p, b.DB, "pa18_types")
		typeID := func(name string) int64 {
			var oid int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT '"+name+"'::regtype::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if oid <= 0 {
				t.Fatal("invalid user type identity", oid)
			}
			return oid
		}
		want := [][]any{
			pgAttributeRow(id, "i", 23, 4, 1, -1, 0, true, "i", "p", false, 0),
			pgAttributeRow(id, "n", 19, 64, 2, -1, 0, false, "c", "p", false, 950),
			pgAttributeRow(id, "t", 25, -1, 3, -1, 0, false, "i", "x", false, 100),
			pgAttributeRow(id, "v", 1043, -1, 4, 11, 0, false, "i", "x", false, 100),
			pgAttributeRow(id, "c", 1042, -1, 5, 7, 0, false, "i", "x", false, 100),
			pgAttributeRow(id, "d", 1700, -1, 6, 524294, 0, false, "i", "m", false, 0),
			pgAttributeRow(id, "u", 2950, 16, 7, -1, 0, false, "c", "p", false, 0),
			pgAttributeRow(id, "j", 1186, 16, 8, -1, 0, false, "d", "p", false, 0),
			pgAttributeRow(id, "s", 1114, 8, 9, 3, 0, true, "d", "p", false, 0),
			pgAttributeRow(id, "b", 1560, -1, 10, 5, 0, false, "i", "x", false, 0),
			pgAttributeRow(id, "a", 1007, -1, 11, -1, 1, false, "i", "x", false, 0),
			pgAttributeRow(id, "f", 1022, -1, 12, -1, 1, false, "d", "x", false, 0),
			pgAttributeRow(id, "acl", 1033, 16, 13, -1, 0, false, "d", "p", false, 0),
			pgAttributeRow(id, "dom", typeID("pa18_type_domain"), 4, 14, -1, 0, true, "i", "p", false, 0),
			pgAttributeRow(id, "e", typeID("pa18_type_enum"), 4, 15, -1, 0, true, "i", "p", false, 0),
			pgAttributeRow(id, "co", typeID("pa18_type_composite"), -1, 16, -1, 0, false, "d", "x", false, 0),
			pgAttributeRow(id, "r", 3904, -1, 17, -1, 0, false, "i", "x", false, 0),
			pgAttributeRow(id, "m", 4451, -1, 18, -1, 0, false, "i", "x", false, 0),
			pgAttributeRow(id, "bc", 1042, -1, 19, 5, 0, false, "i", "x", false, 100),
			pgAttributeRow(id, "bb", 1560, -1, 20, 1, 0, false, "i", "x", false, 0),
		}
		pgAttributeRead(t, p, b, b.DB, id, " AND attnum>0", want)
	})
}

func TestReferencePGAttributeDeclarations(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB,
			"CREATE DOMAIN pa18_rank_domain AS integer[][]",
			"CREATE TYPE pa18_rank_composite AS(a integer[][])",
			"CREATE TABLE pa18_rank(a integer[],b integer[][],c integer[2][3],d pa18_rank_domain)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pa18_rank(a,b,c,d) VALUES('{1}','{2}','{3}','{4}')", 1))
		pgClassExec(t, p, b, b.DB, "CREATE VIEW pa18_rank_view AS SELECT b FROM pa18_rank")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE pa18_rank_ctas AS SELECT b FROM pa18_rank", 1))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE MATERIALIZED VIEW pa18_rank_mv AS SELECT b FROM pa18_rank", 1))
		pgClassExec(t, p, b, b.DB,
			"CREATE TABLE pa18_rank_like(LIKE pa18_rank)",
			"CREATE TABLE pa18_rank_child() INHERITS(pa18_rank)",
			"CREATE TABLE pa18_rank_merged(b integer[][]) INHERITS(pa18_rank)",
			"CREATE TABLE pa18_rank_typed OF pa18_rank_composite")
		indexDDL := "CREATE INDEX pa18_rank_index ON pa18_rank(b) INCLUDE(c)"
		if b.PG {
			pgClassExec(t, p, b, b.DB, indexDDL)
		} else {
			for _, prepared := range []bool{false, true} {
				boundary := fixture.ImplicitError(indexDDL, fixture.Diagnostic{Code: "42704", Message: "data type integer[] has no default operator class for access method \"btree\"", Hint: "You must specify an operator class for the index or define a default operator class for the data type."})
				boundary.Prepared, boundary.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, boundary)
				pgAttributeCheck(t, p, b, b.DB, fixture.Select("SELECT c.relname,a.attname FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.relname='pa18_rank_index'", []string{"relname", "attname"}, []string{"NAME", "NAME"}, nil))
			}
		}
		// An admitted scalar key independently proves native INCLUDE rank without
		// implementing the adjacent array btree operator-class provider.
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pa18_rank_include_heap(id integer,matrix integer[][])", "CREATE INDEX pa18_rank_include ON pa18_rank_include_heap(id) INCLUDE(matrix)")
		includeID := pgAttributeID(t, p, b.DB, "pa18_rank_include")
		pgAttributeRead(t, p, b, b.DB, includeID, "", [][]any{pgAttributeRow(includeID, "id", 23, 4, 1, -1, 0, true, "i", "p", false, 0), pgAttributeRow(includeID, "matrix", 1007, -1, 2, -1, 2, false, "i", "x", false, 0)})
		id := pgAttributeID(t, p, b.DB, "pa18_rank")
		var domain int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'pa18_rank_domain'::regtype::oid").Scan(&domain); err != nil {
			t.Fatal(err)
		}
		want := [][]any{pgAttributeRow(id, "a", 1007, -1, 1, -1, 1, false, "i", "x", false, 0), pgAttributeRow(id, "b", 1007, -1, 2, -1, 2, false, "i", "x", false, 0), pgAttributeRow(id, "c", 1007, -1, 3, -1, 2, false, "i", "x", false, 0), pgAttributeRow(id, "d", domain, -1, 4, -1, 0, false, "i", "x", false, 0)}
		pgAttributeRead(t, p, b, b.DB, id, " AND attnum>0", want)
		for _, tc := range []struct {
			relation, column string
			rank             int64
		}{
			{"pa18_rank_view", "b", 0}, {"pa18_rank_ctas", "b", 0}, {"pa18_rank_mv", "b", 0}, {"pa18_rank_like", "b", 0}, {"pa18_rank_child", "b", 0}, {"pa18_rank_merged", "b", 0}, {"pa18_rank_composite", "a", 2}, {"pa18_rank_typed", "a", 0}, {"pa18_rank_index", "b", 2}, {"pa18_rank_index", "c", 2},
		} {
			t.Run(tc.relation+"/"+tc.column, func(t *testing.T) {
				if !b.PG && tc.relation == "pa18_rank_index" {
					// Exact rejection/absence already asserted above; preserve the
					// full released array-key witnesses on PostgreSQL below.
					return
				}
				q := "SELECT atttypid,atttypmod,attndims FROM pg_attribute WHERE attrelid='" + tc.relation + "'::regclass::oid AND attname='" + tc.column + "'"
				pgAttributeCheck(t, p, b, b.DB, fixture.Select(q, []string{"atttypid", "atttypmod", "attndims"}, []string{"OID", "INT4", "INT2"}, [][]any{{int64(1007), int64(-1), tc.rank}}))
			})
		}
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pa18_rank_alter(a integer[])")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pa18_rank_alter(a) VALUES('{7,8}')", 1))
		pgClassExec(t, p, b, b.DB, "ALTER TABLE pa18_rank_alter ALTER a TYPE integer[][]")
		alter := pgAttributeID(t, p, b.DB, "pa18_rank_alter")
		pgAttributeRead(t, p, b, b.DB, alter, " AND attnum>0", [][]any{pgAttributeRow(alter, "a", 1007, -1, 1, -1, 2, false, "i", "x", false, 0)})
		pgAttributeCheck(t, p, b, b.DB, fixture.Select("SELECT a FROM pa18_rank_alter", []string{"a"}, []string{"_INT4"}, [][]any{{"{7,8}"}}))
		pgClassExec(t, p, b, b.DB, "ALTER TABLE pa18_rank_alter ADD b integer[2][3]")
		pgAttributeRead(t, p, b, b.DB, alter, " AND attnum>0", [][]any{pgAttributeRow(alter, "a", 1007, -1, 1, -1, 2, false, "i", "x", false, 0), pgAttributeRow(alter, "b", 1007, -1, 2, -1, 2, false, "i", "x", false, 0)})
	})
}

func TestReferencePGAttributeFlags(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB,
			"CREATE DOMAIN pa18_flags_domain AS integer",
			"CREATE TABLE pa18_flags(a integer,b integer NOT NULL,c integer PRIMARY KEY,d integer DEFAULT 7,e integer GENERATED ALWAYS AS IDENTITY,f integer GENERATED BY DEFAULT AS IDENTITY,g integer GENERATED ALWAYS AS(a+1) STORED,h integer GENERATED ALWAYS AS(a+2) VIRTUAL,i pa18_flags_domain,j serial)",
			"CREATE TABLE pa18_inh_a(x integer)", "CREATE TABLE pa18_inh_b(x integer)",
			"CREATE TABLE pa18_inh_one() INHERITS(pa18_inh_a)",
			"CREATE TABLE pa18_inh_two() INHERITS(pa18_inh_a,pa18_inh_b)",
			"CREATE TABLE pa18_inh_local(x integer) INHERITS(pa18_inh_a)",
			"CREATE TABLE pa18_flags_root(x integer NOT NULL) PARTITION BY RANGE(x)",
			"CREATE TABLE pa18_flags_leaf PARTITION OF pa18_flags_root FOR VALUES FROM(0) TO(10)")
		id := pgAttributeID(t, p, b.DB, "pa18_flags")
		var domain int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'pa18_flags_domain'::regtype::oid").Scan(&domain); err != nil {
			t.Fatal(err)
		}
		want := make([][]any, 10)
		for i, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
			oid := int64(23)
			if i == 8 {
				oid = domain
			}
			want[i] = pgAttributeRow(id, name, oid, 4, int64(i+1), -1, 0, true, "i", "p", i == 1 || i == 2 || i == 4 || i == 5 || i == 9, 0)
		}
		want[3][12], want[6][12], want[7][12], want[9][12] = true, true, true, true
		want[4][14], want[5][14], want[6][15], want[7][15] = "a", "d", "s", "v"
		pgAttributeRead(t, p, b, b.DB, id, " AND attnum>0", want)
		pgClassExec(t, p, b, b.DB, "CREATE DOMAIN pa18_nn_domain AS integer NOT NULL", "CREATE TABLE pa18_nn_table(a pa18_nn_domain,b pa18_nn_domain NOT NULL)")
		var nnDomain int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'pa18_nn_domain'::regtype::oid").Scan(&nnDomain); err != nil {
			t.Fatal(err)
		}
		nnID := pgAttributeID(t, p, b.DB, "pa18_nn_table")
		pgAttributeRead(t, p, b, b.DB, nnID, " AND attnum>0", [][]any{pgAttributeRow(nnID, "a", nnDomain, 4, 1, -1, 0, true, "i", "p", false, 0), pgAttributeRow(nnID, "b", nnDomain, 4, 2, -1, 0, true, "i", "p", true, 0)})
		for _, tc := range []struct {
			name    string
			local   bool
			parents int64
			nn      bool
		}{
			{"pa18_inh_one", false, 1, false}, {"pa18_inh_two", false, 2, false}, {"pa18_inh_local", true, 1, false}, {"pa18_flags_root", true, 0, true}, {"pa18_flags_leaf", false, 1, true},
		} {
			id := pgAttributeID(t, p, b.DB, tc.name)
			row := pgAttributeRow(id, "x", 23, 4, 1, -1, 0, true, "i", "p", tc.nn, 0)
			row[17], row[18] = tc.local, tc.parents
			pgAttributeRead(t, p, b, b.DB, id, " AND attnum>0", [][]any{row})
		}
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pa18_missing(a integer)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pa18_missing(a) VALUES(1)", 1))
		pgClassExec(t, p, b, b.DB, "ALTER TABLE pa18_missing ADD b integer DEFAULT 7")
		// PG's fast-default history and native materialized fills are distinct.
		missing := [][]any{{false, nil}}
		if b.PG {
			missing = [][]any{{true, "{7}"}}
		}
		s := fixture.Select("SELECT atthasmissing,attmissingval FROM pg_attribute WHERE attrelid='pa18_missing'::regclass::oid AND attname='b'", []string{"atthasmissing", "attmissingval"}, []string{"BOOL", "ANYARRAY"}, missing)
		s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
			if column != 1 || value == nil {
				return false
			}
			if !b.PG {
				t.Fatal("native missing-value carrier must be NULL")
			}
			if _, ok := value.([]byte); !ok {
				t.Fatalf("PG ANYARRAY raw %T", value)
			}
			return true
		}
		pgAttributeCheck(t, p, b, b.DB, s)
	})
}

func TestReferencePGAttributeACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pa18_acl_owner", "pa18_acl_reader", "pa18_acl_observer"})
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE ROLE pa18_acl_owner", "CREATE ROLE pa18_acl_reader", "CREATE ROLE pa18_acl_observer", "CREATE TABLE pa18_acl(a integer,b text)", "ALTER TABLE pa18_acl OWNER TO pa18_acl_owner")
		id := pgAttributeID(t, p, b.DB, "pa18_acl")
		q := "SELECT attname,attacl FROM pg_attribute WHERE attrelid=$1 AND attnum>0 ORDER BY attnum"
		held := fixture.ViewHold(t, p, b, q)
		check := func(db fixture.Executor, acl any) {
			pgAttributeCheck(t, p, b, db, fixture.Select(q, []string{"attname", "attacl"}, []string{"NAME", "_ACLITEM"}, [][]any{{"a", acl}, {"b", nil}}, id))
		}
		check(b.DB, nil)
		pgClassExec(t, p, b, b.DB, "GRANT SELECT(a),UPDATE(a) ON pa18_acl TO pa18_acl_reader WITH GRANT OPTION", "GRANT SELECT(a) ON pa18_acl TO PUBLIC")
		acl := "{pa18_acl_reader=r*w*/pa18_acl_owner,=r/pa18_acl_owner}"
		check(b.DB, acl)
		check(held, acl)
		pgAttributeCheck(t, p, b, b.DB, fixture.Prepared(fixture.Select(q, []string{"attname", "attacl"}, []string{"NAME", "_ACLITEM"}, [][]any{{"a", acl}, {"b", nil}}, id)))
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgClassExec(t, p, b, owner, "SET ROLE pa18_acl_observer")
		defer owner.ExecContext(p.Ctx, "RESET ROLE")
		check(owner, acl)
		pgClassExec(t, p, b, owner, "RESET ROLE")
		pgClassExec(t, p, b, b.DB, "REVOKE UPDATE(a) ON pa18_acl FROM pa18_acl_reader")
		check(held, "{pa18_acl_reader=r*/pa18_acl_owner,=r/pa18_acl_owner}")
		pgClassExec(t, p, b, b.DB, "REVOKE SELECT(a) ON pa18_acl FROM pa18_acl_reader", "REVOKE SELECT(a) ON pa18_acl FROM PUBLIC")
		check(held, nil)
	})
}

func TestReferencePGAttributeSystem(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pa18_system(a integer)", "CREATE TABLE pa18_zero()", "CREATE TABLE pa18_system_root(a integer) PARTITION BY RANGE(a)", "CREATE SEQUENCE pa18_system_seq", "CREATE MATERIALIZED VIEW pa18_system_mv AS SELECT a FROM pa18_system")
		for _, name := range []string{"pa18_system", "pa18_zero", "pa18_system_root", "pa18_system_seq", "pa18_system_mv", "pg_catalog.pg_attribute"} {
			id := pgAttributeID(t, p, b.DB, name)
			pgAttributeRead(t, p, b, b.DB, id, " AND attnum<0", pgAttributeSystemRows(id))
			pgAttributeCheck(t, p, b, b.DB, fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid=$1 AND (attnum=0 OR (attnum<0 AND attname='oid'))", []string{"attname"}, []string{"NAME"}, nil, id))
		}
	})
}

func TestReferencePGAttributeKinds(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgClassExec(t, p, b, b.DB, "CREATE TABLE pa18_kinds(id integer NOT NULL,label varchar(7) COLLATE \"C\",matrix integer[][])", "CREATE VIEW pa18_kinds_view AS SELECT id,label,matrix FROM pa18_kinds", "CREATE TABLE pa18_kinds_ctas AS SELECT id,label,matrix FROM pa18_kinds", "CREATE MATERIALIZED VIEW pa18_kinds_mv AS SELECT id,label,matrix FROM pa18_kinds", "CREATE INDEX pa18_kinds_idx ON pa18_kinds(id) INCLUDE(label)", "CREATE INDEX pa18_kinds_expr ON pa18_kinds((id+1))", "CREATE TYPE pa18_kinds_composite AS(id integer)", "CREATE SEQUENCE pa18_kinds_seq", "CREATE TABLE pa18_kinds_root(id integer) PARTITION BY RANGE(id)")
		partitionIndexDDL := "CREATE INDEX pa18_kinds_pidx ON pa18_kinds_root(id)"
		if b.PG {
			pgClassExec(t, p, b, b.DB, partitionIndexDDL)
			partitionIndexID := pgAttributeID(t, p, b.DB, "pa18_kinds_pidx")
			pgAttributeRead(t, p, b, b.DB, partitionIndexID, " AND attnum<0", nil)
			pgAttributeRead(t, p, b, b.DB, partitionIndexID, " AND attnum>0", [][]any{pgAttributeRow(partitionIndexID, "id", 23, 4, 1, -1, 0, true, "i", "p", false, 0)})
		} else {
			rootID := pgAttributeID(t, p, b.DB, "pa18_kinds_root")
			rootRows := append(pgAttributeSystemRows(rootID), pgAttributeRow(rootID, "id", 23, 4, 1, -1, 0, true, "i", "p", false, 0))
			for _, prepared := range []bool{false, true} {
				boundary := fixture.ImplicitError(partitionIndexDDL, fixture.Diagnostic{Code: "0A000", Message: "partitioned indexes are unsupported"})
				boundary.Prepared, boundary.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, boundary)
				pgAttributeRead(t, p, b, b.DB, rootID, "", rootRows)
				pgAttributeCheck(t, p, b, b.DB, fixture.Select("SELECT c.relname,a.attname FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.relname='pa18_kinds_pidx'", []string{"relname", "attname"}, []string{"NAME", "NAME"}, nil))
			}
		}
		for _, name := range []string{"pa18_kinds_view", "pa18_kinds_composite", "pa18_kinds_idx", "pa18_kinds_expr"} {
			pgAttributeRead(t, p, b, b.DB, pgAttributeID(t, p, b.DB, name), " AND attnum<0", nil)
		}
		for _, name := range []string{"pa18_kinds_view", "pa18_kinds_ctas", "pa18_kinds_mv"} {
			id := pgAttributeID(t, p, b.DB, name)
			pgAttributeRead(t, p, b, b.DB, id, " AND attnum>0", [][]any{pgAttributeRow(id, "id", 23, 4, 1, -1, 0, true, "i", "p", false, 0), pgAttributeRow(id, "label", 1043, -1, 2, 11, 0, false, "i", "x", false, 950), pgAttributeRow(id, "matrix", 1007, -1, 3, -1, 0, false, "i", "x", false, 0)})
		}
		index := pgAttributeID(t, p, b.DB, "pa18_kinds_idx")
		pgAttributeRead(t, p, b, b.DB, index, " AND attnum>0", [][]any{pgAttributeRow(index, "id", 23, 4, 1, -1, 0, true, "i", "p", false, 0), pgAttributeRow(index, "label", 1043, -1, 2, 11, 0, false, "i", "x", false, 0)})
		expr := pgAttributeID(t, p, b.DB, "pa18_kinds_expr")
		pgAttributeRead(t, p, b, b.DB, expr, " AND attnum>0", [][]any{pgAttributeRow(expr, "expr", 23, 4, 1, -1, 0, true, "i", "p", false, 0)})
		seq := pgAttributeID(t, p, b.DB, "pa18_kinds_seq")
		pgAttributeRead(t, p, b, b.DB, seq, " AND attnum>0", [][]any{pgAttributeRow(seq, "last_value", 20, 8, 1, -1, 0, true, "d", "p", true, 0), pgAttributeRow(seq, "log_cnt", 20, 8, 2, -1, 0, true, "d", "p", true, 0), pgAttributeRow(seq, "is_called", 16, 1, 3, -1, 0, true, "c", "p", true, 0)})
	})
}

func TestReferencePGAttributeDeclarationsCopiedRoutes(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		check := func(relation, column string, rank int64) {
			t.Helper()
			t.Run(relation+"/"+column, func(t *testing.T) {
				id := pgAttributeID(t, p, b.DB, relation)
				q := "SELECT atttypid,atttypmod,attndims FROM pg_attribute WHERE attrelid=$1 AND attname=$2"
				s := fixture.Select(q, []string{"atttypid", "atttypmod", "attndims"}, []string{"OID", "INT4", "INT2"}, [][]any{{int64(1007), int64(-1), rank}}, id, column)
				pgAttributeCheck(t, p, b, b.DB, s)
				pgAttributeCheck(t, p, b, b.DB, fixture.Prepared(s))
			})
		}
		t.Run("PartitionOf", func(t *testing.T) {
			pgClassExec(t, p, b, b.DB, "CREATE TABLE pa18_copy_root(id integer,matrix integer[][]) PARTITION BY RANGE(id)", "CREATE TABLE pa18_copy_leaf PARTITION OF pa18_copy_root FOR VALUES FROM(0) TO(10)")
			check("pa18_copy_root", "matrix", 2)
			check("pa18_copy_leaf", "matrix", 0)
		})
		t.Run("TypedAlter", func(t *testing.T) {
			pgClassExec(t, p, b, b.DB, "CREATE TYPE pa18_copy_composite AS(a integer[][])", "CREATE TABLE pa18_copy_typed OF pa18_copy_composite")
			check("pa18_copy_composite", "a", 2)
			check("pa18_copy_typed", "a", 0)
			pgClassExec(t, p, b, b.DB, "ALTER TYPE pa18_copy_composite ALTER ATTRIBUTE a TYPE integer[][] CASCADE")
			check("pa18_copy_composite", "a", 2)
			check("pa18_copy_typed", "a", 2)
			pgClassExec(t, p, b, b.DB, "ALTER TYPE pa18_copy_composite ADD ATTRIBUTE b integer[][] CASCADE")
			check("pa18_copy_composite", "b", 2)
			check("pa18_copy_typed", "b", 2)
			pgClassExec(t, p, b, b.DB, "ALTER TYPE pa18_copy_composite ALTER ATTRIBUTE a TYPE integer[][][] CASCADE")
			check("pa18_copy_composite", "a", 3)
			check("pa18_copy_typed", "a", 3)
		})
	})
}

func TestReferencePGAttributeRepresentedBoundary(t *testing.T) {
	pgAttributeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgAttributeID(t, p, b.DB, "information_schema.table_privileges")
		types := []int64{19, 1043, 1043}
		if b.PG {
			// REL_18_0 information_schema.sql declares these C-collated domains.
			// Their actual identity belongs to this backend, not its allocator.
			for i, name := range []string{"sql_identifier", "character_data", "yes_or_no"} {
				if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'information_schema."+name+"'::regtype::oid").Scan(&types[i]); err != nil || types[i] <= 0 {
					t.Fatal("owning domain identity", name, types[i], err)
				}
			}
		}
		var want [][]any
		for i, name := range []string{"grantor", "grantee", "table_catalog", "table_schema", "table_name", "privilege_type", "is_grantable", "with_hierarchy"} {
			oid, length, typmod, align, storage := types[0], int64(64), int64(-1), "c", "p"
			if i >= 5 {
				oid, length, align, storage = types[1], -1, "i", "x"
			}
			if i >= 6 {
				oid = types[2]
				if !b.PG {
					typmod = 7
				}
			}
			want = append(want, pgAttributeRow(id, name, oid, length, int64(i+1), typmod, 0, false, align, storage, false, 950))
		}
		// Native source16200 owns primitive NAME/VARCHAR usages. This existing
		// bounded representation is not silently relabeled as executable domains.
		pgAttributeRead(t, p, b, b.DB, id, "", want)
	})
}
