package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func networkControlDML(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
	t.Helper()
	var columns, args []string
	values := []any{6}
	for i, tc := range networkCases {
		columns = append(columns, tc.typ)
		args = append(args, "$"+string(rune('2'+i))+"::"+tc.typ)
		values = append(values, tc.boundary)
	}
	q := "INSERT INTO compound_saved(id," + strings.Join(columns, ",") + ") VALUES($1," + strings.Join(args, ",") + ")"
	held := classHeld(t, p, conn, q)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, values...))
	for _, tc := range networkCases {
		classRoutes(t, p, b, conn, ginIDs("SELECT id FROM compound_saved WHERE "+tc.typ+"="+networkLiteral(tc.boundary, tc.typ)+" ORDER BY id", []int64{1, 4, 6}))
		run(fixture.Prepared(fixture.Exec("UPDATE compound_saved SET "+tc.typ+"=$1::"+tc.typ+" WHERE id=6", 1, tc.lower)))
		run(ginIDs("SELECT id FROM compound_saved WHERE "+tc.typ+"<"+networkLiteral(tc.boundary, tc.typ)+" ORDER BY id", []int64{2, 6}))
	}
	run(fixture.Exec("DELETE FROM compound_saved WHERE id=6", 1))
	networkControlCheck(b, run)
}

type networkRetained struct {
	rows     *sql.Rows
	metadata []*sql.ColumnType
	step     fixture.Step
}

func networkRetain(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, table string) []networkRetained {
	t.Helper()
	var retained []networkRetained
	for _, tc := range networkCases {
		s := networkValueStep(b, "SELECT id,v FROM "+table+tc.typ+" ORDER BY id", tc, networkValues(tc))
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
func networkCheckRetained(t *testing.T, retained []networkRetained) {
	t.Helper()
	for _, saved := range retained {
		saved.step.CheckMetadata(t, saved.rows)
		fixture.ScalarAssertDescription(t, saved.metadata, saved.step.Columns, saved.step.Types)
		var got [][]any
		for saved.rows.Next() {
			var id, value any
			if err := saved.rows.Scan(&id, &value); err != nil {
				t.Fatal(err)
			}
			fixture.AssertRawValue(t, false, "INT4", id)
			saved.step.CheckRaw(t, 1, len(got), value)
			got = append(got, []any{fixture.Normalize(id), fixture.Normalize(value)})
		}
		if err := saved.rows.Err(); err != nil {
			t.Fatal(err)
		}
		if _, equal := fixture.CompareRows(got, saved.step.Rows, false); !equal {
			t.Fatalf("retained values=%#v want %#v", got, saved.step.Rows)
		}
		if err := saved.rows.Close(); err != nil {
			t.Fatal(err)
		}
		fixture.ScalarAssertDescription(t, saved.metadata, saved.step.Columns, saved.step.Types)
	}
}

type networkErrorReader struct{ err error }

func (r networkErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceBtreeGINNetworkControls(t *testing.T) {
	for _, relocated := range []bool{false, true} {
		name := "original"
		if relocated {
			name = "relocated"
		}
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"network_controls_owner"})
			fixture.SchemaReferenceCleanup(t, "network_controls_schema")
			var emitted string
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				seed := func() {
					networkControlSeed(run)
					for _, tc := range networkCases {
						table := "network_retained_" + tc.typ
						run(fixture.Exec("CREATE TABLE "+table+"(id integer NOT NULL,v "+tc.typ+")", 0))
						networkSeed(run, table, tc)
						run(fixture.Exec("CREATE INDEX "+table+"_i ON "+table+" USING gin(v "+tc.typ+"_ops)", 0))
					}
					if relocated {
						run(fixture.Exec("CREATE SCHEMA network_controls_schema", 0))
						run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA network_controls_schema", 0))
						run(fixture.Exec("ALTER OPERATOR CLASS network_controls_schema.inet_ops USING gin RENAME TO network_saved_inet", 0))
						run(fixture.Exec("ALTER OPERATOR FAMILY network_controls_schema.cidr_ops USING gin RENAME TO network_saved_cidr_family", 0))
						run(fixture.Exec("CREATE ROLE network_controls_owner", 0))
						run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA network_controls_schema TO network_controls_owner", 0))
						for _, q := range []string{
							"ALTER OPERATOR CLASS network_controls_schema.network_saved_inet USING gin OWNER TO network_controls_owner",
							"ALTER OPERATOR FAMILY network_controls_schema.inet_ops USING gin OWNER TO network_controls_owner",
							"ALTER OPERATOR CLASS network_controls_schema.cidr_ops USING gin OWNER TO network_controls_owner",
							"ALTER OPERATOR FAMILY network_controls_schema.network_saved_cidr_family USING gin OWNER TO network_controls_owner",
							"ALTER FUNCTION network_controls_schema.gin_extract_value_cidr(cidr,internal) OWNER TO network_controls_owner",
						} {
							run(fixture.Exec(q, 0))
						}
					}
				}
				seed()
				networkControlCheck(b, run)
				q := "SELECT id FROM compound_saved WHERE inet >= $1::inet ORDER BY id"
				held := classHeld(t, p, conn, q)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, []int64{1, 4}, networkCases[0].boundary))
				if b.PG {
					// Absence of emitted bytes during native red is not replay evidence.
					run(fixture.Exec("DROP TABLE compound_saved", 0))
					run(fixture.Exec("DROP TYPE compound_saved_enum", 0))
					for _, tc := range networkCases {
						run(fixture.Exec("DROP TABLE network_retained_"+tc.typ, 0))
					}
					run(fixture.Exec("DROP EXTENSION btree_gin CASCADE", 0))
					if relocated {
						run(fixture.Exec("DROP SCHEMA network_controls_schema", 0))
						run(fixture.Exec("DROP ROLE network_controls_owner", 0))
					}
					if emitted != "" {
						// Cluster-global roles are destination prerequisites, not
						// database Dump contents. Replay the emitted bytes unchanged.
						if relocated {
							run(fixture.Exec("CREATE ROLE network_controls_owner", 0))
						}
						if _, err := conn.ExecContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), emitted); err != nil {
							t.Fatalf("PG rejected actual native network Dump: %v\n%s", err, emitted)
						}
					} else {
						seed()
					}
				} else {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					emitted = dump.String()
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO compound_saved(id,inet) VALUES(77,'10.0.0.0/24')", 1))
					run(fixture.Exec("CREATE INDEX network_private_i ON compound_saved USING gin(inet)", 0))
					var committed bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &committed); err != nil || !bytes.Equal(dump.Bytes(), committed.Bytes()) {
						t.Fatalf("private state serialized: %v", err)
					}
					run(fixture.Exec("ROLLBACK", 0))
					retained := networkRetain(t, p, b, conn, "network_retained_")
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					run(amCount("SELECT count(*) FROM pg_extension WHERE extname='btree_gin'", 0))
					run(amCount("SELECT count(*) FROM pg_opclass WHERE opcmethod=2742 AND opcintype IN(869,650,829,774,2950,26)", 0))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN('compound_saved','network_saved_i','network_private_i')", []string{"relname"}, []string{"NAME"}, nil))
					run(fixture.Select("SELECT oid::bigint,amname,amhandler::oid::bigint FROM pg_am WHERE amname='gin'", []string{"oid", "amname", "amhandler"}, []string{"INT8", "NAME", "INT8"}, [][]any{{int64(2742), "gin", int64(333)}}))
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					networkCheckRetained(t, retained)
					networkControlCheck(b, run)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, []int64{1, 4}, networkCases[0].boundary))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, nil, nil))
					connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/network-independent")
					if err != nil {
						t.Fatal(err)
					}
					target := sql.OpenDB(connector)
					defer target.Close()
					recorded, recorder := fixture.RecordedNativePool(t, connector)
					targetRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) }
					if relocated {
						// This Driver owns an independent role domain.
						targetRun(fixture.Exec("CREATE ROLE network_controls_owner", 0))
					}
					if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					networkControlCheck(b, targetRun)
					targetRun(fixture.Exec("INSERT INTO compound_saved(id,inet) VALUES(6,'10.0.0.0/24')", 1))
					targetRun(ginIDs("SELECT id FROM compound_saved WHERE inet='10.0.0.0/24'::inet ORDER BY id", []int64{1, 4, 6}))
					networkControlCheck(b, run)
					targetRun(fixture.Exec("DELETE FROM compound_saved WHERE id=6", 1))
					var redump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, target, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
						t.Fatalf("independent redump: %v", err)
					}
					schema := "public"
					if relocated {
						schema = "network_controls_schema"
					}
					bad := dump.String() + "\nCREATE INDEX network_restore_bad ON public.compound_saved USING gin(oid " + schema + ".oid_ops,uuid " + schema + ".macaddr_ops);"
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
					d := fixture.ControlsReferenceDiagnostic("42804", `operator class "`+schema+`.macaddr_ops" does not accept data type uuid`)
					fixture.AssertDiagnostic(t, err, &d)
					unchanged := func() {
						redump.Reset()
						if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
							t.Fatalf("failed Restore changed destination: %v", err)
						}
					}
					unchanged()
					canceled, cancel := context.WithCancel(p.Ctx)
					err = stuntgres.Restore(canceled, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
					cancel()
					if !errors.Is(err, context.Canceled) {
						t.Fatal("Restore cancellation", err)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatalf("context fabricated SQLSTATE: %v", err)
					}
					unchanged()
					failure := io.ErrUnexpectedEOF
					err = stuntgres.Restore(p.Ctx, p.Native, networkErrorReader{failure})
					if !errors.Is(err, failure) || errors.As(err, &state) {
						t.Fatalf("Restore I/O error nature: %v", err)
					}
					unchanged()
				}
				networkControlCheck(b, run)
				for _, tc := range networkCases {
					classRoutes(t, p, b, conn, networkValueStep(b, "SELECT id,v FROM network_retained_"+tc.typ+" ORDER BY id", tc, networkValues(tc)))
				}
				if !relocated {
					networkGraph(t, p, b, conn, "public")
				} else {
					networkGraph(t, p, b, conn, "network_controls_schema", map[string]string{"class:inet": "network_saved_inet", "family:cidr": "network_saved_cidr_family"})
					classRoutes(t, p, b, conn, fixture.Select("SELECT c.opcowner=r.oid AS class_owner,f.opfowner=r.oid AS family_owner,p.proowner=r.oid AS helper_owner FROM pg_opclass c JOIN pg_opfamily f ON f.oid=c.opcfamily JOIN pg_amproc a ON a.amprocfamily=f.oid AND a.amprocnum=2 JOIN pg_proc p ON p.oid=a.amproc JOIN pg_roles r ON r.rolname='network_controls_owner' WHERE c.opcmethod=2742 AND c.opcintype=650", []string{"class_owner", "family_owner", "helper_owner"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
				}
				networkControlDML(t, p, b, conn, run)
			})
		})
	}
}
