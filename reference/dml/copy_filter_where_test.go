package reference_test

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyFilterWhere(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		for _, format := range []string{"text", "csv"} {
			t.Run(format, func(t *testing.T) {
				c.exec(t, "CREATE TABLE copy_filter(id int, keep bool, note text DEFAULT 'fallback')")
				defer c.exec(t, "DROP TABLE copy_filter")
				c.exec(t, "INSERT INTO copy_filter VALUES (99,true,'sentinel')")
				q := "SELECT id,keep,note FROM copy_filter ORDER BY id"
				conn, stmt := copyFilterHeld(t, p, b, q)
				before := fixture.Select(q, []string{"id", "keep", "note"}, []string{"INT4", "BOOL", "TEXT"}, [][]any{{int64(99), true, "sentinel"}})
				copyFilterConsumers(t, p, b, conn, stmt, before)
				input := "1\ttrue\ta\n2\tfalse\tb\n3\t\\N\tc\n4\ttrue\td\n"
				options := "FORMAT text"
				if format == "csv" {
					input = "id,keep,note\n1,true,a\n2,false,b\n3,,c\n4,true,d\n"
					options = "FORMAT csv, HEADER MATCH"
				}
				copyFilterImport(t, c, "COPY copy_filter FROM STDIN WITH ("+options+") WHERE copy_filter.keep", input, 2)
				after := fixture.Select(q, before.Columns, before.Types, [][]any{{int64(1), true, "a"}, {int64(4), true, "d"}, {int64(99), true, "sentinel"}})
				copyFilterConsumers(t, p, b, conn, stmt, after)
				for _, predicate := range []string{"FALSE", "NULL"} {
					copyFilterImport(t, c, "COPY copy_filter(id,keep) FROM STDIN WHERE "+predicate, "8\ttrue\n", 0)
				}
				copyFilterImport(t, c, "COPY copy_filter(keep,id) FROM STDIN WHERE note='fallback' AND id=5", "true\t5\ntrue\t6\n", 1)
				c.selectRows(t, q, before.Columns, before.Types, [][]any{{int64(1), true, "a"}, {int64(4), true, "d"}, {int64(5), true, "fallback"}, {int64(99), true, "sentinel"}})
				copyFilterNotices(t, *notices)
			})
		}
	})
}

func TestReferenceCopyFilterOrdering(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		t.Run("conversion_before_filter", func(t *testing.T) {
			c.exec(t, "CREATE TABLE copy_order(id int)")
			defer c.exec(t, "DROP TABLE copy_order")
			c.exec(t, "INSERT INTO copy_order VALUES(99)")
			for _, option := range []string{"", " WITH (ON_ERROR stop)"} {
				copyFilterFailure(t, c, "COPY copy_order FROM STDIN"+option+" WHERE FALSE", "1\nbad\n", copyFilterIntError("copy_order", "bad", 2))
				copyFilterIDs(t, c, "copy_order", 99)
			}
			copyFilterImport(t, c, "COPY copy_order FROM STDIN WITH (ON_ERROR ignore) WHERE FALSE", "1\nbad\n", 0)
			copyFilterNotices(t, *notices, copyFilterSummary(1))
			*notices = (*notices)[:0]
			copyFilterFailure(t, c, "COPY copy_order FROM STDIN WITH (ON_ERROR ignore) WHERE 10/id>0", "1\n0\n", fixture.Diagnostic{Code: "22012", Message: "division by zero", Context: "COPY copy_order, line 2: \"0\""})
			copyFilterIDs(t, c, "copy_order", 99)
		})
		t.Run("before_constraints_and_generation", func(t *testing.T) {
			c.exec(t, "CREATE TABLE copy_order(id int PRIMARY KEY, keep bool, v int NOT NULL CHECK(v>0), g int GENERATED ALWAYS AS (10/id) STORED)")
			defer c.exec(t, "DROP TABLE copy_order")
			c.exec(t, "INSERT INTO copy_order(id,keep,v) VALUES(99,true,1)")
			copyFilterImport(t, c, "COPY copy_order(id,keep,v) FROM STDIN WHERE keep", "0\tfalse\t1\n99\tfalse\t1\n2\tfalse\t-1\n3\tfalse\t\\N\n1\ttrue\t1\n", 1)
			c.selectRows(t, "SELECT id,keep,v,g FROM copy_order ORDER BY id", []string{"id", "keep", "v", "g"}, []string{"INT4", "BOOL", "INT4", "INT4"}, [][]any{{int64(1), true, int64(1), int64(10)}, {int64(99), true, int64(1), int64(0)}})
			copyFilterFailure(t, c, "COPY copy_order(id,keep,v) FROM STDIN WITH (ON_ERROR ignore) WHERE keep", "4\ttrue\t1\n0\ttrue\t1\n", fixture.Diagnostic{Code: "22012", Message: "division by zero", Context: "COPY copy_order, line 2: \"0\ttrue\t1\""})
			copyFilterIDs(t, c, "copy_order", 1, 99)
		})
		t.Run("before_partition_admission", func(t *testing.T) {
			c.exec(t, "CREATE TABLE copy_partition(id int) PARTITION BY RANGE(id)")
			defer c.exec(t, "DROP TABLE copy_partition")
			c.exec(t, "CREATE TABLE copy_partition_low PARTITION OF copy_partition FOR VALUES FROM(0) TO(10)")
			copyFilterImport(t, c, "COPY copy_partition FROM STDIN WHERE id<10", "100\n1\n", 1)
			copyFilterIDs(t, c, "copy_partition", 1)
			copyFilterFailure(t, c, "COPY copy_partition FROM STDIN WITH (ON_ERROR ignore) WHERE TRUE", "2\n100\n", fixture.Diagnostic{Code: "23514", Message: "no partition of relation \"copy_partition\" found for row", Detail: "Partition key of the failing row contains (id) = (100).", Context: "COPY copy_partition, line 2: \"100\"", Schema: "public", Table: "copy_partition"})
			copyFilterIDs(t, c, "copy_partition", 1)
		})
	})
}

// Independent binary bytes: signature, zero flags/extension, tuples and trailer.
// No export from either backend supplies the expected input.
func copyFilterBinaryInput() []byte {
	var out bytes.Buffer
	out.WriteString("PGCOPY\n\xff\r\n\x00")
	binary.Write(&out, binary.BigEndian, int32(0))
	binary.Write(&out, binary.BigEndian, int32(0))
	for i := int32(1); i <= 3; i++ {
		binary.Write(&out, binary.BigEndian, int16(2))
		binary.Write(&out, binary.BigEndian, int32(4))
		binary.Write(&out, binary.BigEndian, i)
		if i == 3 {
			binary.Write(&out, binary.BigEndian, int32(-1))
		} else {
			binary.Write(&out, binary.BigEndian, int32(1))
			if i == 1 {
				out.WriteByte(1)
			} else {
				out.WriteByte(0)
			}
		}
	}
	binary.Write(&out, binary.BigEndian, int16(-1))
	return out.Bytes()
}

func TestReferenceCopyFilterBinary(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		c.exec(t, "CREATE TABLE copy_binary_filter(id int, keep bool, note text DEFAULT 'fallback')")
		for _, action := range []string{"", ", ON_ERROR stop"} {
			for _, predicate := range []string{"keep", "TRUE", "FALSE", "NULL"} {
				t.Run(strings.TrimPrefix(action, ", ")+"/"+predicate, func(t *testing.T) {
					c.exec(t, "TRUNCATE copy_binary_filter")
					r, err := c.from("COPY copy_binary_filter(id,keep) FROM STDIN WITH (FORMAT binary"+action+") WHERE "+predicate, bytes.NewReader(copyFilterBinaryInput()))
					count := int64(0)
					if predicate == "keep" {
						count = 1
					}
					if predicate == "TRUE" {
						count = 3
					}
					copyTextSuccess(t, r, err, count)
					want := make([][]any, 0)
					if count >= 1 {
						want = append(want, []any{int64(1), true, "fallback"})
					}
					if count == 3 {
						want = append(want, []any{int64(2), false, "fallback"}, []any{int64(3), nil, "fallback"})
					}
					c.selectRows(t, "SELECT id,keep,note FROM copy_binary_filter ORDER BY id", []string{"id", "keep", "note"}, []string{"INT4", "BOOL", "TEXT"}, want)
				})
			}
		}
		c.exec(t, "TRUNCATE copy_binary_filter")
		r, err := c.from("COPY copy_binary_filter(id,keep) FROM STDIN WITH (FORMAT binary) WHERE note='fallback' AND keep", bytes.NewReader(copyFilterBinaryInput()))
		copyTextSuccess(t, r, err, 1)
		copyFilterIDs(t, c, "copy_binary_filter", 1)
		r, err = c.from("COPY copy_binary_filter(id,keep) FROM STDIN WITH (FORMAT binary,ON_ERROR ignore)", bytes.NewReader(copyFilterBinaryInput()))
		copyTextDiagnostic(t, r, err, fixture.Diagnostic{Code: "42601", Message: "only ON_ERROR STOP is allowed in BINARY mode"})
		copyFilterIDs(t, c, "copy_binary_filter", 1)
		r, err = c.from("COPY copy_binary_filter(id,keep) FROM STDIN WITH (FORMAT binary,ON_ERROR stop) WHERE 10/(id-2)>0", bytes.NewReader(copyFilterBinaryInput()))
		copyTextDiagnostic(t, r, err, fixture.Diagnostic{Code: "22012", Message: "division by zero", Context: "COPY copy_binary_filter, line 2"})
		copyFilterIDs(t, c, "copy_binary_filter", 1)
		c.exec(t, "CREATE TABLE copy_binary_reorder(keep bool,id int,note text DEFAULT 'fallback')")
		r, err = c.from("COPY copy_binary_reorder(id,keep) FROM STDIN WITH (FORMAT binary,ON_ERROR stop) WHERE note='fallback' AND keep", bytes.NewReader(copyFilterBinaryInput()))
		copyTextSuccess(t, r, err, 1)
		c.selectRows(t, "SELECT keep,id,note FROM copy_binary_reorder ORDER BY id", []string{"keep", "id", "note"}, []string{"BOOL", "INT4", "TEXT"}, [][]any{{true, int64(1), "fallback"}})
		copyFilterNotices(t, *notices)
	})
}
