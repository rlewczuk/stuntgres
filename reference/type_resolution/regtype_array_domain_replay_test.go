package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Named associated-array syntax records dimensions zero; it must not become a
// one-dimensional declaration merely to avoid replay's generated-name choices.
func TestReferenceRegtypeArrayDisplacementNamedDomainReplay(t *testing.T) {
	var emitted []byte
	fixture.RegtypeDisplacementScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, q := range []string{
			"CREATE DOMAIN regtype_displacement.payload AS integer",
			"CREATE DOMAIN regtype_displacement.named_zero AS regtype_displacement._payload",
			"CREATE DOMAIN regtype_displacement._payload AS integer",
			"CREATE DOMAIN regtype_displacement.named_after AS regtype_displacement.__payload",
			"CREATE DOMAIN regtype_displacement.rank_two AS regtype_displacement.payload[][]",
			"CREATE TABLE regtype_displacement.named_values(a regtype_displacement.named_zero DEFAULT '[0:2]={1,NULL,2}',b regtype_displacement.named_after DEFAULT '{3,4}',c regtype_displacement.rank_two DEFAULT '{{5,6},{7,NULL}}',r regtype DEFAULT 'regtype_displacement.payload[]'::regtype)",
			"CREATE VIEW regtype_displacement.named_view AS SELECT a,b,c,r FROM regtype_displacement.named_values",
		} {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_displacement.named_values DEFAULT VALUES", 1))
		check := func(p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, postgres bool, count int) {
			if postgres {
				q := "SELECT t.typname,t.typndims::bigint,(t.typbasetype='regtype_displacement.payload[]'::regtype::oid) AS base FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='regtype_displacement' AND t.typname IN ('named_zero','named_after','rank_two') ORDER BY t.typname"
				fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select(q, []string{"typname", "typndims", "base"}, []string{"NAME", "INT8", "BOOL"}, [][]any{{"named_after", int64(0), true}, {"named_zero", int64(0), true}, {"rank_two", int64(2), true}}))
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
				s := fixture.Select(q, []string{"a", "b", "c", "r", "stable"}, []string{family, family, family, "REGTYPE", "BOOL"}, want)
				s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
					if column > 3 {
						return false
					}
					fixture.RegtypeRaw(t, postgres, value)
					return true
				}
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
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
				fixture.RegtypeBoth(t, p, conn, recorder, s)
			}
		}
		check(p, conn, recorder, backend.PG, 1)
		if backend.PG {
			if len(emitted) == 0 {
				t.Fatal("actual named-array native Dump required")
			}
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DROP SCHEMA regtype_displacement CASCADE", 0))
			if _, err := conn.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual named-array PostgreSQL replay", err)
			}
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO regtype_displacement,public", 0))
			check(p, conn, recorder, true, 1)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(p, conn, recorder, false, 1)
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE DOMAIN public.named_array_spacer AS integer", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		targetConn, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer targetConn.Close()
		fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, fixture.Exec("SET search_path TO regtype_displacement,public", 0))
		check(target, targetConn, target.MemoryRecord, false, 1)
		fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, fixture.Exec("INSERT INTO regtype_displacement.named_values DEFAULT VALUES", 1))
		check(target, targetConn, target.MemoryRecord, false, 2)
	})
}

func regtypeDisplacementForeignReplay(t *testing.T, emitted []byte, cases []regtypeDisplacementCase, dataRelations []string) {
	t.Helper()
	target := fixture.NewFixture(t, false, true)
	var targetConn *sql.Conn
	regtypeDisplacementPhase(t, target, "foreign setup", func(phase *fixture.Pair) {
		fixture.RunStep(t, phase.Ctx, phase.Memory, phase.MemoryRecord, fixture.Exec("CREATE DOMAIN public.mixed_spacer AS integer", 0))
		if err := stuntgres.Restore(phase.Ctx, phase.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("mixed foreign Restore", err)
		}
		var err error
		targetConn, err = phase.Memory.Conn(phase.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { targetConn.Close() })
		fixture.RunStep(t, phase.Ctx, targetConn, phase.MemoryRecord, fixture.Exec("SET search_path TO regtype_displacement,public", 0))
	})
	check := func(label string) [][4]int64 {
		var identities [][4]int64
		regtypeDisplacementPhase(t, target, label+" identities", func(phase *fixture.Pair) {
			identities = regtypeDisplacementIdentities(t, phase, targetConn, cases)
		})
		for i, c := range cases {
			regtypeDisplacementPhase(t, target, label+" "+c.base, func(phase *fixture.Pair) {
				regtypeDisplacementLogicalCheck(t, phase, phase.Backends()[0], targetConn, phase.MemoryRecord, c, identities[i])
				named := c
				named.base = c.named
				regtypeDisplacementReferenceCheck(t, phase, targetConn, phase.MemoryRecord, named, identities[i][3], 1)
			})
		}
		for _, name := range dataRelations {
			regtypeDisplacementPhase(t, target, label+" data "+name, func(phase *fixture.Pair) {
				fixture.RegtypeBoth(t, phase, targetConn, phase.MemoryRecord, fixture.Select("SELECT v FROM "+fixture.RegtypeDisplacementQualified(name), []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
		}
		return identities
	}
	beforeFailure := check("foreign initial")
	bad := string(emitted) + "CREATE TABLE regtype_displacement.invalid(v integer DEFAULT 'bad');"
	regtypeDisplacementPhase(t, target, "foreign invalid Restore", func(phase *fixture.Pair) {
		err := stuntgres.Restore(phase.Ctx, phase.Native, strings.NewReader(bad))
		if phase.Ctx.Err() != nil {
			t.Fatalf("late mixed foreign Restore exhausted its phase: %v (Restore: %v)", phase.Ctx.Err(), err)
		}
		// A deadline or unrelated failure must not satisfy failure atomicity.
		got := fixture.ExtractDiagnostic(t, err)
		if got.Code != "22P02" || got.Message != `invalid input syntax for type integer: "bad"` {
			t.Fatalf("late mixed foreign Restore diagnostic: %#v", got)
		}
	})
	if afterFailure := check("foreign after failed Restore"); !reflect.DeepEqual(afterFailure, beforeFailure) {
		t.Fatalf("failed mixed foreign Restore changed identities: got %v want %v", afterFailure, beforeFailure)
	}
	var redump bytes.Buffer
	regtypeDisplacementPhase(t, target, "foreign redump", func(phase *fixture.Pair) {
		if err := stuntgres.Dump(phase.Ctx, phase.Native, &redump); err != nil {
			t.Fatal("mixed foreign redump", err)
		}
	})
	regtypeDisplacementPhase(t, target, "foreign second Restore", func(phase *fixture.Pair) {
		if err := stuntgres.Restore(phase.Ctx, phase.Native, bytes.NewReader(redump.Bytes())); err != nil {
			t.Fatal("mixed foreign second Restore", err)
		}
	})
	identities := check("foreign after second Restore")
	for i, c := range cases {
		for j, element := range []string{c.base, c.named} {
			regtypeDisplacementPhase(t, target, "foreign fresh default "+element, func(phase *fixture.Pair) {
				fixture.RunStep(t, phase.Ctx, targetConn, phase.MemoryRecord, fixture.Exec("INSERT INTO "+fixture.RegtypeDisplacementQualified("w_"+element)+" DEFAULT VALUES", 1))
				probe := c
				probe.base = element
				regtypeDisplacementReferenceCheck(t, phase, targetConn, phase.MemoryRecord, probe, identities[i][1+2*j], 2)
			})
		}
	}
}
