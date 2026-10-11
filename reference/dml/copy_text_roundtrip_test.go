package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

const copyTextGolden = "1\t\\N\t\n2\t\\\\N\tx\\ty\\nz\\r\\\\\n3\té\t\\\\.\n"

func TestReferenceCopyTextRoundTrip(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_source(id int,a text,b text)")
		c.exec(t, "CREATE TABLE copy_destination(id int,a text,b text)")
		c.exec(t, "INSERT INTO copy_source VALUES ($1,$2,$3),($4,$5,$6),($7,$8,$9)", 1, nil, "", 2, `\N`, "x\ty\nz\r\\", 3, "é", `\.`)
		c.output(t, "COPY (SELECT * FROM copy_source ORDER BY id) TO STDOUT", copyTextGolden, 3)
		result, err := c.from("COPY copy_destination FROM STDIN", strings.NewReader(copyTextGolden))
		copyTextSuccess(t, result, err, 3)
		c.selectRows(t, "SELECT * FROM copy_destination ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), nil, ""}, {int64(2), `\N`, "x\ty\nz\r\\"}, {int64(3), "é", `\.`}})
		c.exec(t, "CREATE TABLE copy_handwritten(v text)")
		result, err = c.from("COPY copy_handwritten FROM STDIN", strings.NewReader(`one\ttwo\nthree\rfour\bfive\fsix\vseven\\eight`+"\n"))
		copyTextSuccess(t, result, err, 1)
		c.selectRows(t, "SELECT v FROM copy_handwritten", []string{"v"}, []string{"TEXT"}, [][]any{{"one\ttwo\nthree\rfour\bfive\fsix\vseven\\eight"}})
		c.output(t, "COPY copy_handwritten TO STDOUT", `one\ttwo\nthree\rfour\bfive\fsix\vseven\\eight`+"\n", 1)
	})
}

func TestReferenceCopyTextQueryAndColumns(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_columns(id int,d int DEFAULT 7,v text)")
		result, err := c.from("COPY copy_columns(v,id) FROM STDIN", strings.NewReader("a\t2\nb\t1\n"))
		copyTextSuccess(t, result, err, 2)
		c.selectRows(t, "SELECT * FROM copy_columns ORDER BY id", []string{"id", "d", "v"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(7), "b"}, {int64(2), int64(7), "a"}})
		c.output(t, "COPY (SELECT id+10 AS shifted,v AS label FROM copy_columns ORDER BY id) TO STDOUT WITH (HEADER)", "shifted\tlabel\n11\tb\n12\ta\n", 2)
		c.output(t, "COPY (SELECT id FROM copy_columns WHERE id=99) TO STDOUT", "", 0)
		c.exec(t, "CREATE TABLE copy_parent(id int)")
		c.exec(t, "CREATE TABLE copy_child() INHERITS(copy_parent)")
		c.exec(t, "INSERT INTO copy_parent VALUES(1)")
		c.exec(t, "INSERT INTO copy_child VALUES(2)")
		c.output(t, "COPY copy_parent TO STDOUT", "1\n", 1)
		c.output(t, "COPY (SELECT * FROM copy_child) TO STDOUT", "2\n", 1)
		c.output(t, "COPY (SELECT * FROM copy_parent ORDER BY id) TO STDOUT", "1\n2\n", 2)
		c.exec(t, "CREATE TABLE copy_generated(id int,g int GENERATED ALWAYS AS(id+1) STORED)")
		c.exec(t, "INSERT INTO copy_generated(id) VALUES(1)")
		c.output(t, "COPY copy_generated TO STDOUT", "1\n", 1)
		c.output(t, "COPY (SELECT * FROM copy_generated) TO STDOUT", "1\t2\n", 1)
		c.exec(t, "CREATE VIEW copy_export_view AS SELECT * FROM copy_generated")
		var destination strings.Builder
		result, err = c.to("COPY copy_export_view TO STDOUT", &destination)
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "42809", Message: `cannot copy from view "copy_export_view"`, Hint: "Try the COPY (SELECT ...) TO variant."})
		c.output(t, "COPY (SELECT * FROM copy_export_view) TO STDOUT", "1\t2\n", 1)
		result, err = c.from("COPY copy_export_view FROM STDIN", strings.NewReader("1\t2\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "42809", Message: `cannot copy to view "copy_export_view"`, Hint: "To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		c.exec(t, "CREATE TABLE copy_partition(id int) PARTITION BY RANGE(id)")
		c.exec(t, "CREATE TABLE copy_partition_leaf PARTITION OF copy_partition FOR VALUES FROM(0) TO(10)")
		result, err = c.from("COPY copy_partition FROM STDIN", strings.NewReader("1\n2\n"))
		copyTextSuccess(t, result, err, 2)
		result, err = c.to("COPY copy_partition TO STDOUT", &destination)
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "42809", Message: `cannot copy from partitioned table "copy_partition"`, Hint: "Try the COPY (SELECT ...) TO variant."})
		c.output(t, "COPY (SELECT * FROM copy_partition ORDER BY id) TO STDOUT", "1\n2\n", 2)
		c.exec(t, "CREATE MATERIALIZED VIEW copy_materialized AS SELECT id FROM copy_generated")
		c.output(t, "COPY copy_materialized TO STDOUT", "1\n", 1)
		result, err = c.from("COPY copy_materialized FROM STDIN", strings.NewReader("2\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "42809", Message: `cannot copy to materialized view "copy_materialized"`})
		c.exec(t, "CREATE MATERIALIZED VIEW copy_unpopulated AS SELECT id FROM copy_generated WITH NO DATA")
		result, err = c.to("COPY copy_unpopulated TO STDOUT", &destination)
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "0A000", Message: `cannot copy from unpopulated materialized view "copy_unpopulated"`, Hint: "Use the REFRESH MATERIALIZED VIEW command."})
		for _, target := range []struct{ q, code, message string }{
			{"COPY copy_columns(missing) FROM STDIN", "42703", `column "missing" of relation "copy_columns" does not exist`},
			{"COPY copy_columns(id,id) FROM STDIN", "42701", `column "id" specified more than once`},
		} {
			never := &copyTextNoRead{}
			result, err = c.from(target.q, never)
			copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: target.code, Message: target.message})
			if !b.PG && never.calls != 0 {
				t.Fatal("target validation read caller")
			}
		}
		c.exec(t, "CREATE TABLE copy_zero()")
		result, err = c.from("COPY copy_zero FROM STDIN", strings.NewReader("\n\n"))
		copyTextSuccess(t, result, err, 2)
		c.output(t, "COPY copy_zero TO STDOUT", "\n\n", 2)
		c.output(t, "COPY copy_zero TO STDOUT WITH(HEADER)", "\n\n\n", 2)
	})
}

func TestReferenceCopyTextFeatureSmoke(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_smoke(id int,v text DEFAULT 'fallback')")
		result, err := c.from("COPY copy_smoke FROM STDIN WITH(DELIMITER '|', NULL 'NIL', DEFAULT 'DEF', HEADER MATCH)", strings.NewReader("id|v\n1|DEF\n2|NIL\n3|\n"))
		copyTextSuccess(t, result, err, 3)
		c.output(t, "COPY (SELECT * FROM copy_smoke ORDER BY id) TO STDOUT WITH(DELIMITER '|',NULL 'NIL',HEADER)", "id|v\n1|fallback\n2|NIL\n3|\n", 3)
		c.exec(t, "CREATE TABLE copy_smoke_roundtrip(id int,v text)")
		result, err = c.from("COPY copy_smoke_roundtrip FROM STDIN WITH(DELIMITER '|',NULL 'NIL',HEADER MATCH)", strings.NewReader("id|v\n1|fallback\n2|NIL\n3|\n"))
		copyTextSuccess(t, result, err, 3)
		c.selectRows(t, "SELECT * FROM copy_smoke_roundtrip ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "fallback"}, {int64(2), nil}, {int64(3), ""}})
		result, err = c.from("COPY copy_smoke FROM STDIN", strings.NewReader("4\tok\n5\tbad\textra\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P04", Message: "extra data after last expected column", Context: "COPY copy_smoke, line 2: \"5\tbad\textra\""})
		c.selectRows(t, "SELECT * FROM copy_smoke ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "fallback"}, {int64(2), nil}, {int64(3), ""}})
	})
}
