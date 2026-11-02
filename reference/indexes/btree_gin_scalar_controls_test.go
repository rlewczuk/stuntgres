package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func scalarControlDML(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
	t.Helper()
	q := "INSERT INTO scalar_saved VALUES($1,$2::int2,$3::int4,$4::int8,$5::float4,$6::float8,$7::bool)"
	held := classHeld(t, p, conn, q)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 90, -32768, -2147483648, int64(9007199254740993), "-0", "NaN", true))
	run(ginIDs("SELECT id FROM scalar_saved WHERE int8=9007199254740993 ORDER BY id", []int64{1, 90}))
	run(fixture.Prepared(fixture.Exec("UPDATE scalar_saved SET int8=$1::int8,float8=$2::float8,bool=false WHERE id=90", 1, int64(9007199254740994), "-Infinity")))
	run(ginIDs("SELECT id FROM scalar_saved WHERE float8<'0'::float8 ORDER BY id", []int64{90}))
	run(fixture.Exec("DELETE FROM scalar_saved WHERE id=90", 1))
	scalarControlCheck(t, p, b, conn, run)
}
func scalarRetain(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) []networkRetained {
	t.Helper()
	var retained []networkRetained
	for _, tc := range scalarCases {
		s := scalarValueStep(b, "SELECT id,v FROM scalar_saved_"+tc.typ+" ORDER BY id", tc, scalarRows(b, tc))
		rows, err := conn.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), s.Query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rows.Close() })
		s.CheckMetadata(t, rows)
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		retained = append(retained, networkRetained{rows, metadata, s})
	}
	return retained
}

func scalarCheckRetained(t *testing.T, b fixture.Backend, retained []networkRetained) {
	t.Helper()
	for _, saved := range retained {
		fixture.ScalarAssertDescription(t, saved.metadata, saved.step.Columns, saved.step.Types)
		scalarConsumeValues(t, b, saved.rows, saved.step)
		fixture.ScalarAssertDescription(t, saved.metadata, saved.step.Columns, saved.step.Types)
	}
}
func scalarRelocate(run func(fixture.Step)) {
	run(fixture.Exec("CREATE SCHEMA scalar_controls_schema", 0))
	run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA scalar_controls_schema", 0))
	run(fixture.Exec("ALTER OPERATOR CLASS scalar_controls_schema.int2_ops USING gin RENAME TO scalar_saved_int2", 0))
	run(fixture.Exec("ALTER OPERATOR FAMILY scalar_controls_schema.float8_ops USING gin RENAME TO scalar_saved_float8_family", 0))
	run(fixture.Exec("CREATE ROLE scalar_controls_owner", 0))
	run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA scalar_controls_schema TO scalar_controls_owner", 0))
	for _, q := range []string{
		"ALTER OPERATOR CLASS scalar_controls_schema.scalar_saved_int2 USING gin OWNER TO scalar_controls_owner",
		"ALTER OPERATOR FAMILY scalar_controls_schema.int2_ops USING gin OWNER TO scalar_controls_owner",
		"ALTER OPERATOR CLASS scalar_controls_schema.float8_ops USING gin OWNER TO scalar_controls_owner",
		"ALTER OPERATOR FAMILY scalar_controls_schema.scalar_saved_float8_family USING gin OWNER TO scalar_controls_owner",
		"ALTER FUNCTION scalar_controls_schema.gin_extract_value_float8(float8,internal) OWNER TO scalar_controls_owner",
	} {
		run(fixture.Exec(q, 0))
	}
}
func TestReferenceBtreeGINScalarControls(t *testing.T) {
	for _, relocated := range []bool{false, true} {
		name := "original"
		if relocated {
			name = "relocated"
		}
		t.Run(name, func(t *testing.T) {
			// LIFO: close scenario resources, remove owned schema objects and
			// grants, then remove the cluster-global role, even after Fatal.
			fixture.RoleReferenceCleanup(t, []string{"scalar_controls_owner"})
			fixture.SchemaReferenceCleanup(t, "scalar_controls_schema")
			var emitted string
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				seed := func() {
					scalarControlSeed(run)
					if relocated {
						scalarRelocate(run)
					}
				}
				seed()
				scalarControlCheck(t, p, b, conn, run)
				q := "SELECT id FROM scalar_saved WHERE int8=$1::int8 ORDER BY id"
				held := classHeld(t, p, conn, q)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, []int64{1}, int64(9007199254740993)))
				schema := "public"
				presentation := map[string]string{}
				if relocated {
					schema = "scalar_controls_schema"
					presentation["class:int2"] = "scalar_saved_int2"
					presentation["family:float8"] = "scalar_saved_float8_family"
				}
				if b.PG {
					// A PG seed during native red is expressly not native-Dump replay evidence.
					run(fixture.Exec("DROP TABLE scalar_saved", 0))
					for _, tc := range scalarCases {
						run(fixture.Exec("DROP TABLE scalar_saved_"+tc.typ, 0))
					}
					run(fixture.Exec("DROP TABLE scalar_saved_edges", 0))
					run(fixture.Exec("DROP EXTENSION btree_gin CASCADE", 0))
					if relocated {
						run(fixture.Exec("DROP SCHEMA scalar_controls_schema", 0))
						run(fixture.Exec("DROP ROLE scalar_controls_owner", 0))
					}
					if emitted == "" {
						seed()
					} else {
						if relocated {
							run(fixture.Exec("CREATE ROLE scalar_controls_owner", 0))
						}
						if _, err := conn.ExecContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), emitted); err != nil {
							t.Fatalf("PG rejected actual native scalar Dump: %v\n%s", err, emitted)
						}
					}
				} else {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					emitted = dump.String()
					for _, fragment := range []string{"btree_gin", "1.3", "scalar_saved_i", `"` + schema + `"."`} {
						if !strings.Contains(emitted, fragment) {
							t.Fatalf("actual Dump lacks %q: %s", fragment, emitted)
						}
					}
					for _, tc := range scalarCases {
						class := tc.typ + "_ops"
						if relocated && tc.typ == "int2" {
							class = "scalar_saved_int2"
						}
						if !strings.Contains(emitted, `"`+schema+`"."`+class+`"`) {
							t.Fatalf("Dump lacks current qualified %s.%s", schema, class)
						}
					}
					if relocated && (!strings.Contains(emitted, "scalar_controls_owner") || !strings.Contains(emitted, "scalar_saved_float8_family")) {
						t.Fatal("Dump lost presentation/owner")
					}
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO scalar_saved(id,int8) VALUES(77,9007199254740993)", 1))
					run(fixture.Exec("CREATE INDEX scalar_private_i ON scalar_saved USING gin(int8)", 0))
					var committed bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &committed); err != nil || !bytes.Equal(dump.Bytes(), committed.Bytes()) {
						t.Fatalf("Dump included private state: %v", err)
					}
					run(fixture.Exec("ROLLBACK", 0))
					retained := scalarRetain(t, p, b, conn)
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					run(amCount("SELECT count(*) FROM pg_extension WHERE extname='btree_gin'", 0))
					run(amCount("SELECT count(*) FROM pg_opclass WHERE opcmethod=2742 AND opcintype IN"+scalarInputs, 0))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN('scalar_saved','scalar_saved_i','scalar_private_i')", []string{"relname"}, []string{"NAME"}, nil))
					run(fixture.Select("SELECT oid::bigint,amname FROM pg_am WHERE amname='gin'", []string{"oid", "amname"}, []string{"INT8", "NAME"}, [][]any{{int64(2742), "gin"}}))
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					scalarCheckRetained(t, b, retained)
					scalarControlCheck(t, p, b, conn, run)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, []int64{1}, int64(9007199254740993)))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, nil, nil))
					connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/scalar-independent")
					if err != nil {
						t.Fatal(err)
					}
					target := sql.OpenDB(connector)
					defer target.Close()
					recorded, recorder := fixture.RecordedNativePool(t, connector)
					targetRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) }
					if relocated {
						targetRun(fixture.Exec("CREATE ROLE scalar_controls_owner", 0))
					}
					if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					scalarControlCheck(t, p, fixture.Backend{Recorder: recorder}, recorded, targetRun)
					targetConn, err := recorded.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer targetConn.Close()
					scalarGraph(t, p, fixture.Backend{Recorder: recorder}, targetConn, schema, presentation)
					scalarControlDML(t, p, fixture.Backend{Recorder: recorder}, targetConn, targetRun)
					scalarControlCheck(t, p, b, conn, run)
					var redump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, target, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
						t.Fatalf("independent redump differs: %v", err)
					}
					bad := emitted + "\nCREATE INDEX scalar_restore_bad ON public.scalar_saved USING gin(int8 " + schema + ".int8_ops,float4 " + schema + ".float8_ops);"
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
					d := fixture.ControlsReferenceDiagnostic("42804", `operator class "`+schema+`.float8_ops" does not accept data type real`)
					fixture.AssertDiagnostic(t, err, &d)
					unchanged := func() {
						redump.Reset()
						if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
							t.Fatalf("failed Restore changed target: %v", err)
						}
						compoundIndexAbsent(run, "scalar_saved", "scalar_restore_bad", 1)
					}
					unchanged()
					ctx, cancel := context.WithCancel(p.Ctx)
					err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
					cancel()
					var state interface{ SQLState() string }
					if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
						t.Fatalf("Restore context error nature: %v", err)
					}
					unchanged()
					err = stuntgres.Restore(p.Ctx, p.Native, networkErrorReader{io.ErrUnexpectedEOF})
					if !errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &state) {
						t.Fatalf("Restore reader error nature: %v", err)
					}
					unchanged()
				}
				scalarControlCheck(t, p, b, conn, run)
				scalarGraph(t, p, b, conn, schema, presentation)
				if relocated {
					classRoutes(t, p, b, conn, fixture.Select("SELECT c.opcowner=r.oid AS class_owner,f.opfowner=r.oid AS family_owner,p.proowner=r.oid AS helper_owner FROM pg_opclass c JOIN pg_opfamily f ON f.oid=c.opcfamily JOIN pg_amproc a ON a.amprocfamily=f.oid AND a.amprocnum=2 JOIN pg_proc p ON p.oid=a.amproc JOIN pg_roles r ON r.rolname='scalar_controls_owner' WHERE c.opcmethod=2742 AND c.opcintype=701", []string{"class_owner", "family_owner", "helper_owner"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
				}
				scalarControlDML(t, p, b, conn, run)
			})
		})
	}
}
