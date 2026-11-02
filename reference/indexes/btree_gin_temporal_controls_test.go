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

func temporalControlDML(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
	t.Helper()
	for _, tc := range temporalCases {
		q := "INSERT INTO temporal_saved_" + tc.typ + " VALUES(90,$1::" + tc.typ + ")"
		held := classHeld(t, p, conn, q)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, tc.boundary))
		run(ginIDs("SELECT id FROM temporal_saved_"+tc.typ+" WHERE v=$1::"+tc.typ+" ORDER BY id", []int64{3, 4, 90}, tc.boundary))
		run(fixture.Prepared(fixture.Exec("UPDATE temporal_saved_"+tc.typ+" SET v=$1::"+tc.typ+" WHERE id=90", 1, tc.upper)))
		run(ginIDs("SELECT id FROM temporal_saved_"+tc.typ+" WHERE v=$1::"+tc.typ+" ORDER BY id", []int64{6, 90}, tc.upper))
		run(fixture.Exec("DELETE FROM temporal_saved_"+tc.typ+" WHERE id=90", 1))
	}
	temporalControlCheck(t, p, b, conn, run)
}

func temporalRetain(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) []networkRetained {
	t.Helper()
	var retained []networkRetained
	for _, tc := range temporalCases {
		s := temporalValueStep(b, "SELECT id,v FROM temporal_saved_"+tc.typ+" ORDER BY id", tc, temporalRows(b, tc))
		rows, err := conn.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), s.Query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rows.Close() })
		s.CheckMetadata(t, rows)
		meta, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		retained = append(retained, networkRetained{rows, meta, s})
	}
	return retained
}

func temporalCheckRetained(t *testing.T, b fixture.Backend, retained []networkRetained) {
	t.Helper()
	for _, saved := range retained {
		temporalMetadata(t, b, saved.metadata, saved.step)
		scalarConsumeValues(t, b, saved.rows, saved.step)
		temporalMetadata(t, b, saved.metadata, saved.step)
	}
}

func temporalRelocate(run func(fixture.Step)) {
	run(fixture.Exec("CREATE SCHEMA temporal_controls_schema", 0))
	run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA temporal_controls_schema", 0))
	run(fixture.Exec("ALTER OPERATOR CLASS temporal_controls_schema.date_ops USING gin RENAME TO temporal_saved_date", 0))
	run(fixture.Exec("ALTER OPERATOR FAMILY temporal_controls_schema.interval_ops USING gin RENAME TO temporal_saved_interval_family", 0))
	run(fixture.Exec("CREATE ROLE temporal_controls_owner", 0))
	run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA temporal_controls_schema TO temporal_controls_owner", 0))
	for _, q := range []string{
		"ALTER OPERATOR CLASS temporal_controls_schema.temporal_saved_date USING gin OWNER TO temporal_controls_owner",
		"ALTER OPERATOR FAMILY temporal_controls_schema.date_ops USING gin OWNER TO temporal_controls_owner",
		"ALTER OPERATOR CLASS temporal_controls_schema.interval_ops USING gin OWNER TO temporal_controls_owner",
		"ALTER OPERATOR FAMILY temporal_controls_schema.temporal_saved_interval_family USING gin OWNER TO temporal_controls_owner",
		"ALTER FUNCTION temporal_controls_schema.gin_extract_value_interval(interval,internal) OWNER TO temporal_controls_owner",
	} {
		run(fixture.Exec(q, 0))
	}
}

func TestReferenceBtreeGINTemporalControls(t *testing.T) {
	for _, relocated := range []bool{false, true} {
		name := "original"
		if relocated {
			name = "relocated"
		}
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"temporal_controls_owner"})
			fixture.SchemaReferenceCleanup(t, "temporal_controls_schema")
			var emitted string
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				seed := func() {
					temporalControlSeed(run)
					if relocated {
						temporalRelocate(run)
					}
				}
				seed()
				temporalControlCheck(t, p, b, conn, run)
				queries := make([]string, len(temporalCases))
				held := make([]fixture.HeldStatement, len(temporalCases))
				for i, tc := range temporalCases {
					queries[i] = "SELECT id,v FROM temporal_saved_" + tc.typ + " WHERE v=$1::" + tc.typ + " ORDER BY id"
					held[i] = classHeld(t, p, conn, queries[i])
					fixture.RunStep(t, p.Ctx, held[i], b.Recorder, temporalValueStep(b, queries[i], tc, temporalRows(b, tc)[2:4], tc.boundary))
				}
				schema := "public"
				presentation := map[string]string{}
				if relocated {
					schema = "temporal_controls_schema"
					presentation["class:date"] = "temporal_saved_date"
					presentation["family:interval"] = "temporal_saved_interval_family"
				}
				if b.PG {
					run(fixture.Exec("DROP TABLE temporal_saved", 0))
					for _, tc := range temporalCases {
						run(fixture.Exec("DROP TABLE temporal_saved_"+tc.typ, 0))
					}
					run(fixture.Exec("DROP EXTENSION btree_gin CASCADE", 0))
					if relocated {
						run(fixture.Exec("DROP SCHEMA temporal_controls_schema", 0))
						run(fixture.Exec("DROP ROLE temporal_controls_owner", 0))
					}
					if emitted == "" {
						t.Fatal("native temporal Dump was not produced for PostgreSQL replay")
					}
					if relocated {
						run(fixture.Exec("CREATE ROLE temporal_controls_owner", 0))
					}
					if _, err := conn.ExecContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), emitted); err != nil {
						t.Fatalf("PG rejected actual native temporal Dump: %v\n%s", err, emitted)
					}
				} else {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					emitted = dump.String()
					for _, fragment := range []string{"btree_gin", "1.3", "temporal_saved_i", "fastupdate", "gin_pending_list_limit"} {
						if !strings.Contains(emitted, fragment) {
							t.Fatalf("actual Dump lacks %q", fragment)
						}
					}
					for _, tc := range temporalCases {
						class := tc.typ + "_ops"
						if relocated && tc.typ == "date" {
							class = "temporal_saved_date"
						}
						if !strings.Contains(emitted, `"`+schema+`"."`+class+`"`) {
							t.Fatalf("Dump lacks current qualified %s.%s", schema, class)
						}
					}
					if relocated && (!strings.Contains(emitted, "temporal_controls_owner") || !strings.Contains(emitted, "temporal_saved_interval_family")) {
						t.Fatal("Dump lost presentation/owner")
					}
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO temporal_saved(id,date) VALUES(77,'2000-01-01')", 1))
					run(fixture.Exec("CREATE INDEX temporal_private_i ON temporal_saved USING gin(date)", 0))
					fixture.IntervalDumpMatches(t, p.Ctx, p.Native, dump.Bytes())
					run(fixture.Exec("ROLLBACK", 0))
					retained := temporalRetain(t, p, b, conn)
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					run(amCount("SELECT count(*) FROM pg_extension WHERE extname='btree_gin'", 0))
					run(amCount("SELECT count(*) FROM pg_opclass WHERE opcmethod=2742 AND opcintype IN"+temporalInputs, 0))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN('temporal_saved','temporal_saved_i','temporal_private_i')", []string{"relname"}, []string{"NAME"}, nil))
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					temporalCheckRetained(t, b, retained)
					for i, tc := range temporalCases {
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, temporalValueStep(b, queries[i], tc, temporalRows(b, tc)[2:4], tc.boundary))
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, temporalValueStep(b, queries[i], tc, nil, nil))
					}
					connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/temporal-independent")
					if err != nil {
						t.Fatal(err)
					}
					target := sql.OpenDB(connector)
					defer target.Close()
					recorded, recorder := fixture.RecordedNativePool(t, connector)
					targetRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) }
					if relocated {
						targetRun(fixture.Exec("CREATE ROLE temporal_controls_owner", 0))
					}
					if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					targetConn, err := recorded.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer targetConn.Close()
					targetBackend := fixture.Backend{Recorder: recorder}
					temporalControlCheck(t, p, targetBackend, targetConn, targetRun)
					temporalGraph(t, p, targetBackend, targetConn, schema, presentation)
					temporalControlDML(t, p, targetBackend, targetConn, targetRun)
					fixture.IntervalDumpMatches(t, p.Ctx, target, dump.Bytes())
					temporalControlCheck(t, p, b, conn, run)
					var identities []fixture.Step
					for _, tc := range temporalCases {
						query := "SELECT c.oid::bigint,c.opcfamily::bigint,a.amproc::oid::bigint,i.indexrelid::bigint FROM pg_opclass c JOIN pg_amproc a ON a.amprocfamily=c.opcfamily AND a.amprocnum=2 JOIN pg_index i ON i.indexrelid=$2::regclass WHERE c.opcmethod=2742 AND c.opcintype=$1::oid"
						var classID, familyID, helperID, indexID int64
						table := "temporal_saved_" + tc.typ + "_i"
						if err := conn.QueryRowContext(p.Ctx, query, tc.input, table).Scan(&classID, &familyID, &helperID, &indexID); err != nil {
							t.Fatal(err)
						}
						identities = append(identities, fixture.Select(query, []string{"oid", "opcfamily", "amproc", "indexrelid"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{classID, familyID, helperID, indexID}}, tc.input, table))
					}
					class := "date_ops"
					if relocated {
						class = "temporal_saved_date"
					}
					bad := emitted + "\nCREATE INDEX temporal_restore_bad ON public.temporal_saved USING gin(date " + schema + "." + class + ",timestamp " + schema + ".time_ops);"
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
					d := fixture.ControlsReferenceDiagnostic("42804", `operator class "`+schema+`.time_ops" does not accept data type timestamp without time zone`)
					fixture.AssertDiagnostic(t, err, &d)
					unchanged := func() {
						fixture.IntervalDumpMatches(t, p.Ctx, p.Native, dump.Bytes())
						compoundIndexAbsent(run, "temporal_saved", "temporal_restore_bad", 1)
						temporalControlCheck(t, p, b, conn, run)
						for _, identity := range identities {
							classRoutes(t, p, b, conn, identity)
						}
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
				temporalControlCheck(t, p, b, conn, run)
				temporalGraph(t, p, b, conn, schema, presentation)
				if relocated {
					classRoutes(t, p, b, conn, fixture.Select("SELECT c.opcowner=r.oid AS class_owner,f.opfowner=r.oid AS family_owner,p.proowner=r.oid AS helper_owner FROM pg_opclass c JOIN pg_opfamily f ON f.oid=c.opcfamily JOIN pg_amproc a ON a.amprocfamily=f.oid AND a.amprocnum=2 JOIN pg_proc p ON p.oid=a.amproc JOIN pg_roles r ON r.rolname='temporal_controls_owner' WHERE c.opcmethod=2742 AND c.opcintype=1186", []string{"class_owner", "family_owner", "helper_owner"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
				}
				temporalControlDML(t, p, b, conn, run)
			})
		})
	}
}
