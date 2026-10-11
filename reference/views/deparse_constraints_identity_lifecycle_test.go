package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeparseConstraintsDefinitionIdentities(t *testing.T) {
	fixture.DeparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, pg bool, run func(fixture.Step) fixture.Observed) {
		run(fixture.Exec("CREATE TABLE t(a INT,b BIGINT DEFAULT 7,CONSTRAINT ck CHECK(a::bigint>0))", 0))
		cid := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='ck'")
		rid := fixture.DeparseDefinitionID(t, conn, "SELECT conrelid FROM pg_catalog.pg_constraint WHERE conname='ck'")
		did := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef")
		if cid == rid || cid == did || rid == did {
			t.Fatal("identities collide", cid, rid, did)
		}
		_, relation := deparseReviewTypedExpected(t, pg, "OID", strconv.FormatInt(rid, 10))
		run(fixture.Select("SELECT adrelid,adnum FROM pg_catalog.pg_attrdef", []string{"adrelid", "adnum"}, []string{"OID", "INT2"}, [][]any{{relation, int64(2)}}))
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT pg_get_constraintdef($1,$2)", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"CHECK (a::bigint > 0)"}}, cid, true)
			step.Prepared = prepared
			run(step)
		}
		rows, err := conn.QueryContext(t.Context(), "SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='ck'")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		metadata, err := rows.ColumnTypes()
		wantScan := reflect.TypeFor[string]()
		if pg {
			wantScan = reflect.TypeFor[any]()
		}
		if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != "PG_NODE_TREE" || metadata[0].ScanType() != wantScan {
			t.Fatal(metadata, err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var tree string
		if err := rows.Scan(&tree); err != nil || tree == "" || tree == "((a)::bigint > 0)" || tree == "a::bigint>0" {
			t.Fatal(tree, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestReferenceDeparseConstraintsKindsAndDefaultIdentity(t *testing.T) {
	fixture.DeparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, pg bool, run func(fixture.Step) fixture.Observed) {
		run(fixture.Exec("CREATE TABLE modifiers(a INTEGER CONSTRAINT nn NOT NULL,b INTEGER,CONSTRAINT ck_no CHECK(b>0) NO INHERIT)", 0))
		modifierOID := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_class WHERE relname='modifiers'")
		kind := "CHAR"
		run(fixture.Select("SELECT conname,contype FROM pg_catalog.pg_constraint WHERE conrelid=$1 ORDER BY conname", []string{"conname", "contype"}, []string{"NAME", kind}, [][]any{{"ck_no", "c"}, {"nn", "n"}}, modifierOID))
		run(fixture.Exec("CREATE TABLE defaults(a INT DEFAULT NULL,b INT DEFAULT NULL::integer,c INT DEFAULT COALESCE(NULL::integer,NULL::integer),d INT DEFAULT 7)", 0))
		run(fixture.Select("SELECT adnum FROM pg_catalog.pg_attrdef ORDER BY adnum", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(3)}, {int64(4)}}))
		old := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adnum=4")
		run(fixture.Exec("ALTER TABLE defaults ALTER d SET DEFAULT 3", 0))
		fresh := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adnum=4")
		if fresh == old {
			t.Fatal("replacement retained definition identity")
		}
		run(fixture.Select("SELECT pg_get_expr(adbin,0) FROM pg_catalog.pg_attrdef WHERE adnum=4", []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{"3"}}))
		run(fixture.Exec("ALTER TABLE defaults ALTER d SET DEFAULT NULL", 0))
		run(fixture.Select("SELECT adnum FROM pg_catalog.pg_attrdef", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(3)}}))
		run(fixture.Exec("ALTER TABLE defaults ALTER d SET DEFAULT 7", 0))
		if fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adnum=4") == fresh {
			t.Fatal("re-add reused identity")
		}
	})
}
