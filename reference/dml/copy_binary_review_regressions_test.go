package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"stuntgres"
	"stuntgres/reference/fixture"
)

// Every frame in this review suite is independently constructed. Complete
// malformed-input records come only from the pinned PostgreSQL discovery,
// never from native failures or development-source diagnostic predictions.
func TestReferenceCopyBinaryReviewRegressions(t *testing.T) {
	t.Run("NumericSpecialRanges", copyBinaryReviewNumericSpecialRanges)
	t.Run("NestedDiagnostics", copyBinaryReviewNestedDiagnostics)
	t.Run("MultirangeOrder", copyBinaryReviewMultirangeOrder)
	t.Run("PlainDomains", copyBinaryReviewPlainDomains)
	t.Run("OutputProviders", copyBinaryReviewOutputProviders)
	t.Run("Controls", copyBinaryReviewControls)
}

func copyBinaryReviewNumericSpecialRanges(t *testing.T) {
	for _, bound := range []struct{ name, wire, text string }{
		{"positive_infinity", "00000000d0000020", "Infinity"},
		{"negative_infinity", "00000000f0000020", "-Infinity"},
		{"nan", "00000000c0000000", "NaN"},
	} {
		for _, multi := range []bool{false, true} {
			typ, databaseType := "numrange", "NUMRANGE"
			payload := copyBinaryRange(0x12, copyBinaryHex(bound.wire))
			text := "[" + bound.text + ",)"
			if multi {
				typ, databaseType = "nummultirange", "NUMMULTIRANGE"
				payload = copyBinaryMultirange(payload)
				text = "{" + text + "}"
			}
			t.Run(typ+"/"+bound.name, func(t *testing.T) {
				copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
					c.exec(t, "CREATE TABLE copy_binary_probe(v "+typ+")")
					input := copyBinaryStream(copyBinaryRow(payload))
					result, err := c.from("COPY copy_binary_probe FROM STDIN WITH(FORMAT binary)", bytes.NewReader(input))
					copyTextSuccess(t, result, err, 1)
					copyBinaryReviewReads(t, c, "SELECT v FROM copy_binary_probe", "v", databaseType, []any{text})
					c.output(t, "COPY copy_binary_probe TO STDOUT", text+"\n", 1)
					c.output(t, "COPY copy_binary_probe TO STDOUT WITH(FORMAT binary)", string(input), 1)
				})
			})
		}
	}
	// Both finite-infinity endpoints remain real bounds, not unbounded flags.
	t.Run("numrange/closed_infinities", func(t *testing.T) {
		copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
			c.exec(t, "CREATE TABLE copy_binary_probe(v numrange)")
			payload := copyBinaryRange(0x06, copyBinaryHex("00000000f0000020"), copyBinaryHex("00000000d0000020"))
			input := copyBinaryStream(copyBinaryRow(payload))
			result, err := c.from("COPY copy_binary_probe FROM STDIN WITH(FORMAT binary)", bytes.NewReader(input))
			copyTextSuccess(t, result, err, 1)
			copyBinaryRawProvider(t, c, copyBinaryProviderExpected{databaseType: "NUMRANGE", rawKind: "string", raw: "[-Infinity,Infinity]"})
			c.output(t, "COPY copy_binary_probe TO STDOUT", "[-Infinity,Infinity]\n", 1)
			c.output(t, "COPY copy_binary_probe TO STDOUT WITH(FORMAT binary)", string(input), 1)
		})
	})
}

// Complete records transcribed only from root pinned tests-14 PostgreSQL JSON.
var copyBinaryReviewDiagnostics = map[string]fixture.Diagnostic{
	"array_utf8_before_missing_later":                   {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: "invalid byte sequence for encoding \"UTF8\": 0xff", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
	"array_declared_member_truncated":                   {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P03", Message: "insufficient data left in message", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
	"record_declared_member_truncated":                  {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P03", Message: "insufficient data left in message", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
	"array_varchar_expected_timestamp_supplied":         {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "binary data has array element type 1114 (timestamp without time zone) instead of expected 1043 (character varying)", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
	"array_timestamp_expected_varchar_supplied":         {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "binary data has array element type 1043 (character varying) instead of expected 1114 (timestamp without time zone)", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
	"record_varchar_expected_timestamp_supplied":        {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "binary data has type 1114 (timestamp without time zone) instead of expected 1043 (character varying) in record column 1", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
	"record_timestamp_expected_varchar_supplied":        {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "binary data has type 1043 (character varying) instead of expected 1114 (timestamp without time zone) in record column 1", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
	"record_timetz_expected_time_supplied":              {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "binary data has type 1083 (time without time zone) instead of expected 1266 (time with time zone) in record column 1", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
	"record_time_expected_timetz_supplied":              {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "binary data has type 1266 (time with time zone) instead of expected 1083 (time without time zone) in record column 1", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
	"record_integer_array_expected_text_array_supplied": {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "binary data has type 1009 (text[]) instead of expected 1007 (integer[]) in record column 1", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
	"record_text_array_expected_integer_array_supplied": {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "binary data has type 1007 (integer[]) instead of expected 1009 (text[]) in record column 1", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
}

func copyBinaryReviewNestedDiagnostics(t *testing.T) {
	// Complete first TEXT input must be received before a missing later length.
	utf8First := append(copyBinaryHex("0000000100000000000000190000000200000001"), copyBinary32(1)...)
	utf8First = append(utf8First, 0xff)
	// Declared child len4, but only one payload byte: not complete int4 len1.
	arrayTruncated := copyBinaryHex("00000001000000000000001700000001000000010000000400")
	recordTruncated := copyBinaryHex("00000001000000170000000400")
	cases := []struct {
		name, typ string
		setup     []string
		payload   []byte
	}{
		{"array_utf8_before_missing_later", "text[]", nil, utf8First},
		{"array_declared_member_truncated", "int4[]", nil, arrayTruncated},
		{"record_declared_member_truncated", "copy_review_record", []string{"CREATE TYPE copy_review_record AS(i int4)"}, recordTruncated},
		{"array_varchar_expected_timestamp_supplied", "varchar[]", nil, copyBinaryArray(1114, [][2]int32{{1, 1}}, []byte{})},
		{"array_timestamp_expected_varchar_supplied", "timestamp[]", nil, copyBinaryArray(1043, [][2]int32{{1, 1}}, []byte{})},
		{"record_varchar_expected_timestamp_supplied", "copy_review_record", []string{"CREATE TYPE copy_review_record AS(v varchar)"}, copyBinaryComposite([]int32{1114}, []byte{})},
		{"record_timestamp_expected_varchar_supplied", "copy_review_record", []string{"CREATE TYPE copy_review_record AS(v timestamp)"}, copyBinaryComposite([]int32{1043}, []byte{})},
		{"record_timetz_expected_time_supplied", "copy_review_record", []string{"CREATE TYPE copy_review_record AS(v timetz)"}, copyBinaryComposite([]int32{1083}, []byte{})},
		{"record_time_expected_timetz_supplied", "copy_review_record", []string{"CREATE TYPE copy_review_record AS(v time)"}, copyBinaryComposite([]int32{1266}, []byte{})},
		{"record_integer_array_expected_text_array_supplied", "copy_review_record", []string{"CREATE TYPE copy_review_record AS(v int4[])"}, copyBinaryComposite([]int32{1009}, []byte{})},
		{"record_text_array_expected_integer_array_supplied", "copy_review_record", []string{"CREATE TYPE copy_review_record AS(v text[])"}, copyBinaryComposite([]int32{1007}, []byte{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
				for _, q := range tc.setup {
					c.exec(t, q)
				}
				c.exec(t, "CREATE TABLE copy_binary_probe(v "+tc.typ+")")
				result, err := c.from("COPY copy_binary_probe FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow(tc.payload))))
				want, ok := copyBinaryReviewDiagnostics[tc.name]
				if !ok {
					t.Fatalf("missing pinned review diagnostic %q", tc.name)
				}
				copyTextDiagnostic(t, result, err, want)
				copyBinaryCount(t, c, 0)
			})
		})
	}
}

func copyBinaryReviewPlainDomains(t *testing.T) {
	setup := []string{"CREATE DOMAIN copy_plain AS int4"}
	array := func(t *testing.T, c copyTextConnection) []byte {
		return copyBinaryArray(copyBinaryTypeOID(t, c, "copy_plain"), [][2]int32{{2, 1}}, copyBinary32(42), nil)
	}
	cases := []copyBinaryProviderCase{
		{name: "plain_scalar", typ: "copy_plain", setup: setup, payload: copyBinary32(42), valid: true, wire: copyBinary32(42)},
		{name: "plain_scalar_null", typ: "copy_plain", setup: setup, payload: nil, valid: true},
		{name: "plain_scalar_short_base", typ: "copy_plain", setup: setup, payload: []byte{0}},
		{name: "plain_array", typ: "copy_plain[]", setup: setup, valid: true, identity: true, build: array},
		{name: "plain_array_null", typ: "copy_plain[]", setup: setup, payload: nil, valid: true},
		{name: "plain_array_empty", typ: "copy_plain[]", setup: setup, valid: true, identity: true, build: func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryArray(copyBinaryTypeOID(t, c, "copy_plain"), nil)
		}},
		{name: "plain_array_short_base", typ: "copy_plain[]", setup: setup, build: func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryArray(copyBinaryTypeOID(t, c, "copy_plain"), [][2]int32{{1, 1}}, []byte{0})
		}},
		{name: "plain_array_builtin_wire_oid", typ: "copy_plain[]", setup: setup, valid: true, build: func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryArray(23, [][2]int32{{2, 1}}, copyBinary32(42), nil)
		}, expectedBuild: array},
		{name: "plain_array_other_domain_wire_oid", typ: "copy_plain[]", setup: []string{"CREATE DOMAIN copy_plain AS int4", "CREATE DOMAIN copy_other_plain AS int4"}, valid: true, build: func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryArray(copyBinaryTypeOID(t, c, "copy_other_plain"), [][2]int32{{2, 1}}, copyBinary32(42), nil)
		}, expectedBuild: array},
	}
	short := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "08P01", Message: "insufficient data left in message", Context: "COPY copy_binary_probe, line 1, column v"}
	expected := map[string]copyBinaryProviderExpected{
		"plain_scalar":                      {text: "42\n", databaseType: "INT4", rawKind: "int64", raw: int64(42)},
		"plain_scalar_null":                 {text: "\\N\n", databaseType: "INT4", rawKind: "null"},
		"plain_scalar_short_base":           {diagnostic: &short},
		"plain_array":                       {text: "{42,NULL}\n", databaseType: "$_copy_plain", rawKind: "string", raw: "{42,NULL}"},
		"plain_array_null":                  {text: "\\N\n", databaseType: "$_copy_plain", rawKind: "null"},
		"plain_array_empty":                 {text: "{}\n", databaseType: "$_copy_plain", rawKind: "string", raw: "{}"},
		"plain_array_short_base":            {diagnostic: &short},
		"plain_array_builtin_wire_oid":      {text: "{42,NULL}\n", databaseType: "$_copy_plain", rawKind: "string", raw: "{42,NULL}"},
		"plain_array_other_domain_wire_oid": {text: "{42,NULL}\n", databaseType: "$_copy_plain", rawKind: "string", raw: "{42,NULL}"},
	}
	copyBinaryProviders(t, cases, expected)
	t.Run("prepared_scalar_and_array", func(t *testing.T) {
		copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
			c.exec(t, "CREATE DOMAIN copy_plain AS int4")
			c.exec(t, "CREATE TABLE copy_binary_plain_prepared(d copy_plain,a copy_plain[])")
			input := copyBinaryStream(copyBinaryRow(copyBinary32(42), array(t, c)), copyBinaryRow(nil, nil))
			result, err := c.from("COPY copy_binary_plain_prepared FROM STDIN WITH(FORMAT binary)", bytes.NewReader(input))
			copyTextSuccess(t, result, err, 2)
			arrayType := "_COPY_PLAIN"
			if c.pg {
				arrayType = strconv.FormatInt(int64(copyBinaryTypeOID(t, c, "_copy_plain")), 10)
			}
			copyBinaryReviewReads(t, c, "SELECT d AS v FROM copy_binary_plain_prepared ORDER BY d NULLS LAST", "v", "INT4", []any{int64(42), nil})
			copyBinaryReviewReads(t, c, "SELECT a AS v FROM copy_binary_plain_prepared ORDER BY d NULLS LAST", "v", arrayType, []any{"{42,NULL}", nil})
			c.output(t, "COPY (SELECT * FROM copy_binary_plain_prepared ORDER BY d NULLS LAST) TO STDOUT WITH(FORMAT binary)", string(input), 2)
		})
	})
}

// Assert raw values and metadata before normalization on both public query
// routes; byte carriers are deliberately distinct from equivalent strings.
func copyBinaryReviewReads(t *testing.T, c copyTextConnection, query, column, databaseType string, want []any) {
	t.Helper()
	check := func(rows *sql.Rows) {
		defer rows.Close()
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, []string{column}) {
			t.Fatal("review columns", names, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != databaseType {
			t.Fatal("review metadata", types, err, databaseType)
		}
		for _, expected := range want {
			if !rows.Next() {
				t.Fatal("missing review row", rows.Err())
			}
			var actual any
			if err := rows.Scan(&actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("review raw=%T %#v want=%T %#v", actual, actual, expected, expected)
			}
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("review row multiplicity/end", rows.Err())
		}
	}
	rows, err := c.conn.QueryContext(c.ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	check(rows)
	held, err := c.conn.PrepareContext(c.ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	rows, err = held.QueryContext(c.ctx)
	if err != nil {
		t.Fatal(err)
	}
	check(rows)
}

func copyBinaryReviewOutputProviders(t *testing.T) {
	t.Run("catalog_char", func(t *testing.T) {
		copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
			query := "SELECT relkind AS v FROM pg_class WHERE oid=2605"
			copyBinaryReviewReads(t, c, query, "v", "CHAR", []any{"r"})
			c.output(t, "COPY ("+query+") TO STDOUT WITH(FORMAT binary)", string(copyBinaryStream(copyBinaryRow([]byte{'r'}))), 1)
			c.output(t, "COPY ("+query+") TO STDOUT", "r\n", 1)
		})
	})
	t.Run("admitted_xid_carrier", func(t *testing.T) {
		copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
			// Existing CTAS carrier admission, not newly enabled raw XID DDL.
			c.exec(t, "CREATE TABLE copy_binary_xid AS SELECT 0::int4 AS id,transaction AS x FROM pg_catalog.pg_prepared_xacts WHERE false")
			c.exec(t, "INSERT INTO copy_binary_xid VALUES(0,'0'),(1,'4294967295'),(2,NULL),(3,'17')")
			query := "SELECT x FROM copy_binary_xid ORDER BY id"
			want := []any{[]byte("0"), []byte("4294967295"), nil, []byte("17")}
			if c.pg {
				want = []any{int64(0), int64(4294967295), nil, int64(17)}
			}
			copyBinaryReviewReads(t, c, query, "x", "XID", want)
			wire := copyBinaryStream(copyBinaryRow(copyBinaryHex("00000000")), copyBinaryRow(copyBinaryHex("ffffffff")), copyBinaryRow(nil), copyBinaryRow(copyBinaryHex("00000011")))
			c.output(t, "COPY ("+query+") TO STDOUT WITH(FORMAT binary)", string(wire), 4)
			c.output(t, "COPY ("+query+") TO STDOUT", "0\n4294967295\n\\N\n17\n", 4)
		})
	})
}

func copyBinaryReviewControls(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		ddl := []string{"CREATE DOMAIN copy_plain AS int4", "CREATE TABLE copy_binary_review_controls(r numrange,m nummultirange,d copy_plain,a copy_plain[])"}
		positive := copyBinaryRange(0x12, copyBinaryHex("00000000d0000020"))
		negative := copyBinaryRange(0x12, copyBinaryHex("00000000f0000020"))
		input := func(t *testing.T, c copyTextConnection) []byte {
			return copyBinaryStream(copyBinaryRow(positive, copyBinaryMultirange(negative), copyBinary32(42), copyBinaryArray(copyBinaryTypeOID(t, c, "copy_plain"), [][2]int32{{2, 1}}, copyBinary32(42), nil)))
		}
		seed := func(t *testing.T, c copyTextConnection) {
			for _, q := range ddl {
				c.exec(t, q)
			}
			result, err := c.from("COPY copy_binary_review_controls FROM STDIN WITH(FORMAT binary)", bytes.NewReader(input(t, c)))
			copyTextSuccess(t, result, err, 1)
		}
		check := func(t *testing.T, c copyTextConnection) {
			types := []string{"NUMRANGE", "NUMMULTIRANGE", "INT4", "_COPY_PLAIN"}
			if c.pg {
				types[3] = strconv.FormatInt(int64(copyBinaryTypeOID(t, c, "_copy_plain")), 10)
			}
			c.selectRows(t, "SELECT * FROM copy_binary_review_controls", []string{"r", "m", "d", "a"}, types, [][]any{{"[Infinity,)", "{[-Infinity,)}", int64(42), "{42,NULL}"}})
			c.output(t, "COPY copy_binary_review_controls TO STDOUT WITH(FORMAT binary)", string(input(t, c)), 1)
			c.output(t, "COPY copy_binary_review_controls TO STDOUT", "[Infinity,)\t{[-Infinity,)}\t42\t{42,NULL}\n", 1)
		}
		seed(t, c)
		check(t, c)
		native := c
		if b.PG {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			conn, err := p.Native.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			native = copyTextConnection{conn: conn, ctx: p.Ctx}
			seed(t, native)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(t, native)
		connector, err := stuntgres.NewDriver().OpenConnector("reference/binary-review-restored/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		freshDB := sql.OpenDB(connector)
		defer freshDB.Close()
		if err := stuntgres.Restore(p.Ctx, freshDB, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		freshConn, err := freshDB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer freshConn.Close()
		check(t, copyTextConnection{conn: freshConn, ctx: p.Ctx})
		if b.PG {
			c.exec(t, "DROP TABLE copy_binary_review_controls")
			c.exec(t, "DROP DOMAIN copy_plain")
			c.exec(t, dump.String())
			// Recreated domain arrays have new OIDs. This checks fresh consumers,
			// not pgx's retained implicit prepared-result identity.
			if err := c.conn.Raw(func(raw any) error {
				return raw.(*stdlib.Conn).Conn().DeallocateAll(c.ctx)
			}); err != nil {
				t.Fatal(err)
			}
		}
		check(t, c)
	})
}

// Complete PostgreSQL records independently observed in root tests-18.
var copyBinaryReviewMultirangeDiagnostics = map[string]fixture.Diagnostic{
	"reversed_first_missing_later": {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22000", Message: "range lower bound must be less than or equal to range upper bound", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 2, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
	"empty_first_missing_later":    {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "08P01", Message: "no data left in message", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 2, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""},
}

func copyBinaryReviewMultirangeOrder(t *testing.T) {
	for _, tc := range []struct{ name, wire string }{
		{"reversed_first_missing_later", "00000006000000110200000004000000020000000400000001"},
		{"empty_first_missing_later", "0000000200000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
				c.exec(t, "CREATE TABLE copy_binary_probe(v int4multirange)")
				c.exec(t, "INSERT INTO copy_binary_probe VALUES('{[99,100)}')")
				valid := copyBinaryMultirange(copyBinaryRange(2, copyBinary32(1), copyBinary32(3)))
				input := copyBinaryStream(copyBinaryRow(valid), copyBinaryRow(copyBinaryHex(tc.wire)))
				result, err := c.from("COPY copy_binary_probe FROM STDIN WITH(FORMAT binary)", bytes.NewReader(input))
				want, ok := copyBinaryReviewMultirangeDiagnostics[tc.name]
				if !ok {
					t.Fatalf("missing pinned multirange diagnostic %q", tc.name)
				}
				copyTextDiagnostic(t, result, err, want)
				copyBinaryCount(t, c, 1)
				c.selectRows(t, "SELECT v FROM copy_binary_probe", []string{"v"}, []string{"INT4MULTIRANGE"}, [][]any{{"{[99,100)}"}})
				sentinel := copyBinaryStream(copyBinaryRow(copyBinaryMultirange(copyBinaryRange(2, copyBinary32(99), copyBinary32(100)))))
				c.output(t, "COPY copy_binary_probe TO STDOUT WITH(FORMAT binary)", string(sentinel), 1)
			})
		})
	}
}
