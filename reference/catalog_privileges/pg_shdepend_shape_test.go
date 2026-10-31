package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func shMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 7 {
		t.Fatal(types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if pg && i != 3 {
			scan = reflect.TypeFor[any]()
		} else if pg {
			scan = reflect.TypeFor[int32]()
		} else if i == 6 {
			scan = reflect.TypeFor[string]()
		}
		if typ.Name() != shColumns[i] || typ.DatabaseTypeName() != shTypes[i] || typ.ScanType() != scan {
			t.Fatal(i, typ, typ.ScanType())
		}
		if nullable, known := typ.Nullable(); nullable || known == pg {
			t.Fatal("nullability", i, nullable, known)
		}
		if n, ok := typ.Length(); n != 0 || ok {
			t.Fatal("length", i)
		}
		if p, s, ok := typ.DecimalSize(); p != 0 || s != 0 || ok {
			t.Fatal("precision", i)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for i, typ := range types {
		if typ.DatabaseTypeName() != shTypes[i] {
			t.Fatal("retained descriptor")
		}
	}
}

func TestReferencePGShdependShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.Select("SELECT oid,relname,relnamespace,relkind,relnatts,relisshared,relhasindex,reltype FROM pg_catalog.pg_class WHERE oid=1214", []string{"oid", "relname", "relnamespace", "relkind", "relnatts", "relisshared", "relhasindex", "reltype"}, []string{"OID", "NAME", "OID", "CHAR", "INT2", "BOOL", "BOOL", "OID"}, [][]any{{int64(1214), "pg_shdepend", int64(11), "r", int64(7), true, true, int64(10060)}}))
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.Select("SELECT oid,typrelid,typarray FROM pg_type WHERE oid=10060", []string{"oid", "typrelid", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(10060), int64(1214), int64(10059)}}))
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.Select("SELECT oid,typelem FROM pg_type WHERE oid=10059", []string{"oid", "typelem"}, []string{"OID", "OID"}, [][]any{{int64(10059), int64(10060)}}))
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.Select("SELECT attname,atttypid,attnotnull,atttypmod,attcollation FROM pg_attribute WHERE attrelid=1214 AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "attnotnull", "atttypmod", "attcollation"}, []string{"NAME", "OID", "BOOL", "INT4", "OID"}, [][]any{{"dbid", int64(26), true, int64(-1), int64(0)}, {"classid", int64(26), true, int64(-1), int64(0)}, {"objid", int64(26), true, int64(-1), int64(0)}, {"objsubid", int64(23), true, int64(-1), int64(0)}, {"refclassid", int64(26), true, int64(-1), int64(0)}, {"refobjid", int64(26), true, int64(-1), int64(0)}, {"deptype", int64(18), true, int64(-1), int64(0)}}))
	}
	for _, b := range p.Backends() {
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_shdepend WHERE FALSE", "SELECT * FROM pg_catalog.pg_shdepend WHERE objid=4294967295::oid", "SELECT * FROM pg_catalog.pg_shdepend WHERE objid=$1"} {
			var args []any
			if q == "SELECT * FROM pg_catalog.pg_shdepend WHERE objid=$1" {
				args = []any{nil}
			}
			rows, err := b.DB.QueryContext(p.Ctx, q, args...)
			if err != nil {
				t.Fatal(err)
			}
			shMetadata(t, rows, b.PG)
			for _, prepared := range []bool{false, true} {
				s := shSelect(q, [][]any{}, args...)
				s.Prepared = prepared
				fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
			}
		}
	}
}

func TestReferencePGShdependQueryRoutes(t *testing.T) {
	p := shScenario(t)
	for _, b := range p.Backends() {
		db, table, owner, _, _ := shSetup(t, p, b)
		q := "SELECT * FROM pg_catalog.pg_shdepend WHERE dbid=$1 AND classid=1259 AND objid=$2 AND refobjid=$3"
		s := shSelect(q, [][]any{shEdge(db, 1259, table, 0, owner, "o")}, db, table, owner)
		for _, prepared := range []bool{false, true} {
			for _, plain := range []bool{false, true} {
				s.Prepared = prepared
				s.NonContext = plain
				fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
			}
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		s.Prepared = false
		s.NonContext = false
		fixture.PgDescriptionExecutorRows(t, p, b, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, s)
		rows, err := stmt.Query(db, table, owner)
		if err != nil {
			t.Fatal(err)
		}
		shRetained(t, rows, [][]any{shEdge(db, 1259, table, 0, owner, "o")})
		stmt.Close()
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT c.relname,r.rolname,d.deptype FROM pg_catalog.pg_shdepend d JOIN pg_class c ON c.oid=d.objid JOIN pg_roles r ON r.oid=d.refobjid JOIN pg_database b ON b.oid=d.dbid WHERE d.classid=1259 AND d.objid=$1 AND b.datname=current_database() ORDER BY r.rolname", []string{"relname", "rolname", "deptype"}, []string{"NAME", "NAME", "CHAR"}, [][]any{{"sh_table", "sh_owner", "o"}}, table))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		shExec(t, p, b, conn, "CREATE TABLE public.pg_shdepend(marker TEXT)", "SET search_path TO public,pg_catalog")
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT * FROM pg_shdepend", []string{"marker"}, []string{"TEXT"}, [][]any{}))
		fixture.PgDescriptionExecutorRows(t, p, b, conn, shSelect("SELECT * FROM pg_catalog.pg_shdepend WHERE FALSE", [][]any{}))
		shExec(t, p, b, conn, "RESET search_path")
		conn.Close()
	}
}
