package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"
)

func deparseDefinitionID(t *testing.T, conn *sql.Conn, query string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := conn.QueryRowContext(t.Context(), query, args...).Scan(&id); err != nil || id <= 0 || id > 4294967295 {
		t.Fatal(query, id, err)
	}
	return id
}

func TestReferenceDeparseConstraintsDefinitionIdentities(t *testing.T) {
	deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, pg bool, run func(referenceStep) referenceObserved) {
		run(referenceExec("CREATE TABLE t(a INT,b BIGINT DEFAULT 7,CONSTRAINT ck CHECK(a::bigint>0))", 0))
		cid := deparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='ck'")
		rid := deparseDefinitionID(t, conn, "SELECT conrelid FROM pg_catalog.pg_constraint WHERE conname='ck'")
		did := deparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef")
		if cid == rid || cid == did || rid == did {
			t.Fatal("identities collide", cid, rid, did)
		}
		_, relation := deparseReviewTypedExpected(t, pg, "OID", strconv.FormatInt(rid, 10))
		run(referenceSelect("SELECT adrelid,adnum FROM pg_catalog.pg_attrdef", []string{"adrelid", "adnum"}, []string{"OID", "INT2"}, [][]any{{relation, int64(2)}}))
		for _, prepared := range []bool{false, true} {
			step := referenceSelect("SELECT pg_get_constraintdef($1,$2)", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"CHECK (a::bigint > 0)"}}, cid, true)
			step.prepared = prepared
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
	deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, pg bool, run func(referenceStep) referenceObserved) {
		run(referenceExec("CREATE TABLE modifiers(a INTEGER CONSTRAINT nn NOT NULL,b INTEGER,CONSTRAINT ck_no CHECK(b>0) NO INHERIT)", 0))
		modifierOID := deparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_class WHERE relname='modifiers'")
		kind := "CHAR"
		run(referenceSelect("SELECT conname,contype FROM pg_catalog.pg_constraint WHERE conrelid=$1 ORDER BY conname", []string{"conname", "contype"}, []string{"NAME", kind}, [][]any{{"ck_no", "c"}, {"nn", "n"}}, modifierOID))
		run(referenceExec("CREATE TABLE defaults(a INT DEFAULT NULL,b INT DEFAULT NULL::integer,c INT DEFAULT COALESCE(NULL::integer,NULL::integer),d INT DEFAULT 7)", 0))
		run(referenceSelect("SELECT adnum FROM pg_catalog.pg_attrdef ORDER BY adnum", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(3)}, {int64(4)}}))
		old := deparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adnum=4")
		run(referenceExec("ALTER TABLE defaults ALTER d SET DEFAULT 3", 0))
		fresh := deparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adnum=4")
		if fresh == old {
			t.Fatal("replacement retained definition identity")
		}
		run(referenceSelect("SELECT pg_get_expr(adbin,0) FROM pg_catalog.pg_attrdef WHERE adnum=4", []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{"3"}}))
		run(referenceExec("ALTER TABLE defaults ALTER d SET DEFAULT NULL", 0))
		run(referenceSelect("SELECT adnum FROM pg_catalog.pg_attrdef", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(3)}}))
		run(referenceExec("ALTER TABLE defaults ALTER d SET DEFAULT 7", 0))
		if deparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adnum=4") == fresh {
			t.Fatal("re-add reused identity")
		}
	})
}
