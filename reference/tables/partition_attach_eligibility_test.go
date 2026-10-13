package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func partitionAttachEligibilityDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		route := "direct"
		if prepared {
			route = "prepared"
		}
		for _, name := range []string{"temporary", "invalid_populated_reordered", "invalid_empty_sparse", "valid_reordered", "invalid_type_precedence", "invalid_name_precedence", "invalid_null_bound", "invalid_draw_bound"} {
			t.Run("eligibility/"+name+"/"+route, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						conn, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE pa_elig_parent(k INT,payload INT CONSTRAINT pa_elig_parent_nn NOT NULL) PARTITION BY RANGE(k)", 0))
						run(fixture.Exec("CREATE TABLE pa_elig_old PARTITION OF pa_elig_parent FOR VALUES FROM(20) TO(30)", 0))
						run(fixture.Exec("INSERT INTO pa_elig_parent VALUES(21,210)", 1))
						create := "CREATE TABLE pa_elig_child(payload INT,k INT)"
						switch name {
						case "temporary":
							create = "CREATE TEMP TABLE pa_elig_child(payload INT CONSTRAINT pa_elig_child_nn NOT NULL,k INT)"
						case "invalid_empty_sparse":
							create = "CREATE TABLE pa_elig_child(gone INT,payload INT,k INT)"
						case "valid_reordered":
							create = "CREATE TABLE pa_elig_child(payload INT CONSTRAINT pa_elig_child_nn NOT NULL,k INT)"
						case "invalid_type_precedence":
							create = "CREATE TABLE pa_elig_child(payload BIGINT,k INT)"
						case "invalid_name_precedence":
							create = "CREATE TABLE pa_elig_child(payload INT,j INT)"
						}
						run(fixture.Exec(create, 0))
						if name == "invalid_empty_sparse" {
							run(fixture.Exec("ALTER TABLE pa_elig_child DROP COLUMN gone", 0))
						}
						rows := [][]any{}
						if name == "invalid_populated_reordered" {
							run(fixture.Exec("INSERT INTO pa_elig_child VALUES(NULL,1)", 1))
							rows = [][]any{{nil, int64(1)}}
						} else if name == "temporary" || name == "valid_reordered" {
							run(fixture.Exec("INSERT INTO pa_elig_child VALUES(10,1)", 1))
							rows = [][]any{{int64(10), int64(1)}}
						}
						valid := name == "temporary" || name == "valid_reordered"
						if !valid {
							run(fixture.Exec("ALTER TABLE pa_elig_child ADD CONSTRAINT pa_elig_child_nn NOT NULL payload NOT VALID", 0))
						}
						var oidBefore string
						if err := conn.QueryRowContext(p.Ctx, "SELECT 'pa_elig_child'::regclass::OID::TEXT").Scan(&oidBefore); err != nil {
							t.Fatal(err)
						}
						var nnBefore string
						if err := conn.QueryRowContext(p.Ctx, "SELECT oid::TEXT FROM pg_constraint WHERE conrelid='pa_elig_child'::regclass::OID AND conname='pa_elig_child_nn'").Scan(&nnBefore); err != nil {
							t.Fatal(err)
						}
						provenance := func(local, validated bool, inherited int64) {
							run(fixture.Select("SELECT conname,contype,conislocal,coninhcount,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='pa_elig_child'::regclass::OID ORDER BY conname", []string{"conname", "contype", "conislocal", "coninhcount", "definition"}, []string{"NAME", "CHAR", "BOOL", "INT2", "TEXT"}, [][]any{{"pa_elig_child_nn", "n", local, inherited, partitionAttachEligibilityNNDefinition(validated)}}))
							if b.PG {
								// convalidated is an existing native catalog-column boundary;
								// the shared deparser still independently preserves NOT VALID.
								run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid='pa_elig_child'::regclass::OID AND conname='pa_elig_child_nn'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{validated}}))
							}
						}
						provenance(true, valid, 0)
						lower := "1"
						want := fixture.Diagnostic{Code: "42P17", Message: `constraint "pa_elig_child_nn" conflicts with NOT VALID constraint on child table "pa_elig_child"`}
						switch name {
						case "temporary":
							want = fixture.Diagnostic{Code: "42809", Message: `cannot attach a temporary relation as partition of permanent relation "pa_elig_parent"`}
						case "invalid_type_precedence":
							want = fixture.Diagnostic{Code: "42804", Message: `child table "pa_elig_child" has different type for column "payload"`}
						case "invalid_name_precedence":
							want = fixture.Diagnostic{Code: "42804", Message: `table "pa_elig_child" contains column "j" not found in parent "pa_elig_parent"`, Detail: "The new partition may contain only the columns present in parent."}
						case "invalid_null_bound":
							lower = "NULL"
							want = fixture.Diagnostic{Code: "42P17", Message: "cannot specify NULL in range bound"}
						case "invalid_draw_bound":
							run(fixture.Exec("CREATE SEQUENCE pa_elig_seq START 1", 0))
							lower = "nextval('pa_elig_seq')"
						}
						q := "ALTER TABLE pa_elig_parent ATTACH PARTITION pa_elig_child FOR VALUES FROM(" + lower + ") TO(3)"
						s := fixture.ImplicitError(q, want)
						if name == "valid_reordered" {
							s = fixture.Exec(q, 0)
						}
						s.Prepared = prepared
						t.Run("diagnostic", func(t *testing.T) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) })
						if name == "invalid_draw_bound" {
							run(fixture.Select("SELECT last_value,is_called FROM pa_elig_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), true}}))
						}
						key := "k"
						if name == "invalid_name_precedence" {
							key = "j"
						}
						payloadType := "INT4"
						if name == "invalid_type_precedence" {
							payloadType = "INT8"
						}
						run(fixture.Select("SELECT * FROM pa_elig_child", []string{"payload", key}, []string{payloadType, "INT4"}, rows))
						parentRows := [][]any{{int64(21), int64(210)}}
						attached := name == "valid_reordered"
						if attached {
							parentRows = [][]any{{int64(1), int64(10)}, {int64(21), int64(210)}}
							provenance(false, true, 1)
						} else {
							provenance(true, valid, 0)
						}
						run(fixture.Select("SELECT * FROM pa_elig_parent ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, parentRows))
						run(fixture.Select("SELECT relispartition FROM pg_class WHERE oid='pa_elig_child'::regclass::OID", []string{"relispartition"}, []string{"BOOL"}, [][]any{{attached}}))
						assertPartitionAttachEligibilityOID(t, p, conn, oidBefore, nnBefore)
						if !attached {
							run(fixture.Exec("INSERT INTO pa_elig_child VALUES(90,9)", 1))
							rows = append(rows, []any{int64(90), int64(9)})
							run(fixture.Select("SELECT * FROM pa_elig_child ORDER BY "+key, []string{"payload", key}, []string{payloadType, "INT4"}, rows))
							run(fixture.Select("SELECT * FROM pa_elig_parent ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, parentRows))
						}
					})
				}
			})
		}
	}
}

func assertPartitionAttachEligibilityOID(t *testing.T, p *fixture.Pair, conn *sql.Conn, before, nnBefore string) {
	t.Helper()
	var after string
	if err := conn.QueryRowContext(p.Ctx, "SELECT 'pa_elig_child'::regclass::OID::TEXT").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("candidate OID changed: before %s after %s", before, after)
	}
	var nnAfter string
	if err := conn.QueryRowContext(p.Ctx, "SELECT oid::TEXT FROM pg_constraint WHERE conrelid='pa_elig_child'::regclass::OID AND conname='pa_elig_child_nn'").Scan(&nnAfter); err != nil {
		t.Fatal(err)
	}
	if nnAfter != nnBefore {
		t.Fatalf("NOT NULL constraint OID changed: before %s after %s", nnBefore, nnAfter)
	}
}

func partitionAttachEligibilityNNDefinition(validated bool) string {
	if validated {
		return "NOT NULL payload"
	}
	return "NOT NULL payload NOT VALID"
}
