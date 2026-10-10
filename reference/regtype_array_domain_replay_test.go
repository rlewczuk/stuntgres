package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

// Named associated-array syntax records dimensions zero; it must not become a
// one-dimensional declaration merely to avoid replay's generated-name choices.
func TestReferenceRegtypeArrayDisplacementNamedDomainReplay(t *testing.T) {
	var emitted []byte
	regtypeDisplacementScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder) {
		for _, q := range []string{
			"CREATE DOMAIN regtype_displacement.payload AS integer",
			"CREATE DOMAIN regtype_displacement.named_zero AS regtype_displacement._payload",
			"CREATE DOMAIN regtype_displacement._payload AS integer",
			"CREATE DOMAIN regtype_displacement.named_after AS regtype_displacement.__payload",
			"CREATE DOMAIN regtype_displacement.rank_two AS regtype_displacement.payload[][]",
			"CREATE TABLE regtype_displacement.named_values(a regtype_displacement.named_zero DEFAULT '[0:2]={1,NULL,2}',b regtype_displacement.named_after DEFAULT '{3,4}',c regtype_displacement.rank_two DEFAULT '{{5,6},{7,NULL}}',r regtype DEFAULT 'regtype_displacement.payload[]'::regtype)",
			"CREATE VIEW regtype_displacement.named_view AS SELECT a,b,c,r FROM regtype_displacement.named_values",
		} {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, 0))
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO regtype_displacement.named_values DEFAULT VALUES", 1))
		check := func(p *referencePair, conn *sql.Conn, recorder *referenceRecorder, postgres bool, count int) {
			if postgres {
				q := "SELECT t.typname,t.typndims::bigint,(t.typbasetype='regtype_displacement.payload[]'::regtype::oid) AS base FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='regtype_displacement' AND t.typname IN ('named_zero','named_after','rank_two') ORDER BY t.typname"
				regtypeBoth(t, p, conn, recorder, referenceSelect(q, []string{"typname", "typndims", "base"}, []string{"NAME", "INT8", "BOOL"}, [][]any{{"named_after", int64(0), true}, {"named_zero", int64(0), true}, {"rank_two", int64(2), true}}))
			}
			family := "__PAYLOAD"
			if postgres {
				family = ""
			}
			want := make([][]any, count)
			for i := range want {
				want[i] = []any{"[0:2]={1,NULL,2}", "{3,4}", "{{5,6},{7,NULL}}", "payload[]", true}
			}
			for _, source := range []string{"named_values", "named_view"} {
				q := "SELECT a,b,c,r,(r::oid='regtype_displacement.payload[]'::regtype::oid) AS stable FROM regtype_displacement." + source
				s := referenceSelect(q, []string{"a", "b", "c", "r", "stable"}, []string{family, family, family, "REGTYPE", "BOOL"}, want)
				s.checkRaw = func(t *testing.T, column, row int, value any) bool {
					if column > 3 {
						return false
					}
					regtypeRaw(t, postgres, value)
					return true
				}
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for _, column := range columns[:4] {
						scan := reflect.TypeFor[string]()
						if postgres {
							scan = reflect.TypeFor[any]()
						}
						nullable, known := column.Nullable()
						length, lengthOK := column.Length()
						precision, scale, decimalOK := column.DecimalSize()
						if column.ScanType() != scan || nullable != !postgres || known != !postgres || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK {
							t.Fatalf("named-array metadata %s scan=%v nullable=%t/%t size=%d/%t decimal=%d,%d/%t", column.Name(), column.ScanType(), nullable, known, length, lengthOK, precision, scale, decimalOK)
						}
					}
				}
				regtypeBoth(t, p, conn, recorder, s)
			}
		}
		check(p, conn, recorder, backend.pg, 1)
		if backend.pg {
			if len(emitted) == 0 {
				t.Fatal("actual named-array native Dump required")
			}
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("DROP SCHEMA regtype_displacement CASCADE", 0))
			if _, err := conn.ExecContext(p.ctx, string(emitted)); err != nil {
				t.Fatal("actual named-array PostgreSQL replay", err)
			}
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO regtype_displacement,public", 0))
			check(p, conn, recorder, true, 1)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(p, conn, recorder, false, 1)
		target := newReferenceFixture(t, false, true)
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec("CREATE DOMAIN public.named_array_spacer AS integer", 0))
		if err := stuntgres.Restore(target.ctx, target.native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		targetConn, err := target.memory.Conn(target.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer targetConn.Close()
		referenceRunStep(t, target.ctx, targetConn, target.memoryRecord, referenceExec("SET search_path TO regtype_displacement,public", 0))
		check(target, targetConn, target.memoryRecord, false, 1)
		referenceRunStep(t, target.ctx, targetConn, target.memoryRecord, referenceExec("INSERT INTO regtype_displacement.named_values DEFAULT VALUES", 1))
		check(target, targetConn, target.memoryRecord, false, 2)
	})
}

func regtypeDisplacementForeignReplay(t *testing.T, emitted []byte, cases []regtypeDisplacementCase, dataRelations []string) {
	t.Helper()
	target := newReferenceFixture(t, false, true)
	referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec("CREATE DOMAIN public.mixed_spacer AS integer", 0))
	if err := stuntgres.Restore(target.ctx, target.native, bytes.NewReader(emitted)); err != nil {
		t.Fatal("mixed foreign Restore", err)
	}
	targetConn, err := target.memory.Conn(target.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer targetConn.Close()
	referenceRunStep(t, target.ctx, targetConn, target.memoryRecord, referenceExec("SET search_path TO regtype_displacement,public", 0))
	check := func() {
		for _, c := range cases {
			regtypeDisplacementLogicalCheck(t, target, target.backends()[0], targetConn, target.memoryRecord, c)
			named := c
			named.base = c.named
			regtypeDisplacementReferenceCheck(t, target, targetConn, target.memoryRecord, named, regtypeDisplacementOID(t, target, targetConn, c.named+"[]"), 1)
		}
		for _, name := range dataRelations {
			regtypeBoth(t, target, targetConn, target.memoryRecord, referenceSelect("SELECT v FROM "+regtypeDisplacementQualified(name), []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
	}
	check()
	bad := string(emitted) + "CREATE TABLE regtype_displacement.invalid(v integer DEFAULT 'bad');"
	if err := stuntgres.Restore(target.ctx, target.native, strings.NewReader(bad)); err == nil {
		t.Fatal("late mixed foreign Restore succeeded")
	}
	check()
	var redump bytes.Buffer
	if err := stuntgres.Dump(target.ctx, target.native, &redump); err != nil {
		t.Fatal("mixed foreign redump", err)
	}
	if err := stuntgres.Restore(target.ctx, target.native, bytes.NewReader(redump.Bytes())); err != nil {
		t.Fatal("mixed foreign second Restore", err)
	}
	check()
	for _, c := range cases {
		for _, element := range []string{c.base, c.named} {
			referenceRunStep(t, target.ctx, targetConn, target.memoryRecord, referenceExec("INSERT INTO "+regtypeDisplacementQualified("w_"+element)+" DEFAULT VALUES", 1))
			probe := c
			probe.base = element
			regtypeDisplacementReferenceCheck(t, target, targetConn, target.memoryRecord, probe, regtypeDisplacementOID(t, target, targetConn, element+"[]"), 2)
		}
	}
}
