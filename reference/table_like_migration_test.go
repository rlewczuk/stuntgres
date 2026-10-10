package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceTableLikeMigratedStoredTypes(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE supported_jsonb(v JSONB)", 0))
		run(referenceExec(`INSERT INTO supported_jsonb VALUES('{"a":1,"a":2}')`, 1))
		run(referenceExec(`CREATE TABLE supported_uuid(v "uuid")`, 0))
		run(referenceExec(`INSERT INTO supported_uuid VALUES('{A0EEBC999C0B4EF8BB6D6BB9BD380A11}')`, 1))
		run(referenceExec("CREATE TABLE supported_zoned(z TIMESTAMPTZ,q TIMETZ)", 0))
		run(referenceExec("INSERT INTO supported_zoned VALUES('2004-01-01 12:00+02','12:00+02')", 1))
		run(referenceExec(`CREATE TABLE raw(i "int4" NOT NULL,b "int8",s "text",f "bool",n "int2")`, 0))
		run(referenceSelect("SELECT * FROM raw", []string{"i", "b", "s", "f", "n"}, []string{"INT4", "INT8", "TEXT", "BOOL", "INT2"}, nil))
		run(referenceExec("CREATE TABLE typed(i INT4 NOT NULL,b INT8,s TEXT,f BOOL)", 0))
		run(referenceExec("INSERT INTO typed VALUES($1,$2,$3,$4)", 1, 7, int64(9223372036854775807), "O'Brien", true))
		run(referenceSelect("SELECT * FROM typed", []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), "O'Brien", true}}))
		referenceTableDDLMetadata(t, p.ctx, c, b.pg, prepared, "SELECT * FROM typed", []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true}, 1)
		for _, name := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
			run(tableLikeError("CREATE TABLE bad("+name+" INT)", referenceDiagnostic{Code: "42701", Message: `column name "` + name + `" conflicts with a system column name`}))
		}
		run(referenceExec(`CREATE TABLE ordinary(oid INT,"CTID" TEXT)`, 0))
		run(referenceSelect("SELECT * FROM ordinary", []string{"oid", "CTID"}, []string{"INT4", "TEXT"}, nil))
	})
}
