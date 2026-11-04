package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const hbaSelect = "SELECT rule_number,file_name,line_number,type,database,user_name,address,netmask,auth_method,options,error FROM pg_catalog.pg_hba_file_rules"
const hbaSSLWarning = "hostssl record cannot match because SSL is disabled"
const hbaCanonical = "#c\nlocal all all ident map=old map=new\ninclude {ROOT}/child.conf\nhost all all 192.0.2.129/24 scram-sha-256\nbad all all trust\n"

var hbaColumns = []string{"rule_number", "file_name", "line_number", "type", "database", "user_name", "address", "netmask", "auth_method", "options", "error"}
var hbaTypes = []string{"INT4", "TEXT", "INT4", "TEXT", "_TEXT", "_TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "TEXT"}

// hbaArray only quotes literal independent expectations; it does not inspect SQL.
func hbaArray(values ...string) any {
	if len(values) == 0 {
		return nil
	}
	quoted := make([]string, len(values))
	for i, s := range values {
		quoted[i] = s
		if s == "" || strings.EqualFold(s, "NULL") || strings.ContainsAny(s, "{},\"\\ \t\n\r\v\f") {
			quoted[i] = "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(s) + "\""
		}
	}
	return "{" + strings.Join(quoted, ",") + "}"
}
func hbaValid(n int64, file string, line int64, typ string, db, users any, address, mask any, method string, opts ...string) []any {
	return []any{n, file, line, typ, db, users, address, mask, method, hbaArray(opts...), nil}
}
func hbaLocal(n int64, file string, line int64, method string, opts ...string) []any {
	return hbaValid(n, file, line, "local", hbaArray("all"), hbaArray("all"), nil, nil, method, opts...)
}
func hbaInvalid(file string, line int64, message string) []any {
	return []any{nil, file, line, nil, nil, nil, nil, nil, nil, nil, message}
}
func hbaArtifact(n int64, file string, line int64) []any {
	return []any{n, file, line, nil, nil, nil, nil, nil, nil, nil, nil}
}
func hbaMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, hbaColumns) {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 11 {
		t.Fatal(types, err)
	}
	for i, c := range types {
		if c.Name() != hbaColumns[i] || c.DatabaseTypeName() != hbaTypes[i] {
			t.Fatal(i, c)
		}
		nullable, known := c.Nullable()
		if pg && (nullable || known) || !pg && (!nullable || !known) {
			t.Fatal(i, nullable, known)
		}
		precision, scale, known := c.DecimalSize()
		if precision != 0 || scale != 0 || known {
			t.Fatal(i, precision, scale, known)
		}
		length, known := c.Length()
		if hbaTypes[i] == "TEXT" {
			if length != math.MaxInt64 || !known || c.ScanType() != reflect.TypeFor[string]() {
				t.Fatal(i, length, known, c.ScanType())
			}
		} else if known {
			t.Fatal(i, length)
		}
		if hbaTypes[i] == "_TEXT" {
			want := reflect.TypeFor[string]()
			if pg {
				want = reflect.TypeFor[any]()
			}
			if c.ScanType() != want || length != 0 {
				t.Fatal(i, c.ScanType(), length)
			}
		}
	}
}
func hbaRead(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, want [][]any, prepared bool) {
	t.Helper()
	s := fixture.Select(query, hbaColumns, hbaTypes, want)
	s.Prepared, s.Bag = prepared, !strings.Contains(strings.ToUpper(query), "ORDER BY")
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { hbaMetadata(t, rows, b.PG) }
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}
func hbaCanonicalRows(f *fixture.HBAFiles, b fixture.Backend) [][]any {
	main := f.Path(b, "")
	return [][]any{
		hbaLocal(1, main, 2, "peer", "map=new"),
		hbaValid(2, f.Path(b, "child.conf"), 1, "host", hbaArray("db1", "db2"), hbaArray("+staff", ".user"), ".example.test", nil, "trust"),
		hbaValid(3, main, 4, "host", hbaArray("all"), hbaArray("all"), "192.0.2.129", "255.255.255.0", "scram-sha-256"),
		hbaInvalid(main, 5, `invalid connection type "bad"`),
	}
}
func TestReferencePGHBAFileRulesRows(t *testing.T) {
	p, f := fixture.NewHBAFileScenario(t, hbaCanonicalSet())
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			want := hbaCanonicalRows(f, b)
			for _, prepared := range []bool{false, true} {
				hbaRead(t, p, b, b.DB, hbaSelect, want, prepared)
				hbaRead(t, p, b, b.DB, "SELECT * FROM pg_hba_file_rules", want, prepared)
				hbaRead(t, p, b, b.DB, hbaSelect+" WHERE error IS NULL ORDER BY rule_number", want[:3], prepared)
				if !b.PG {
					s := fixture.Exec(hbaSelect, 4)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			hbaRepresentativeRows(t, p, f, b)
			if !b.PG {
				hbaArbitraryRows(t, p, f, b)
			}
		})
	}
}

// Released warning and artifact rows prove fields are independently nullable.
func hbaRepresentativeRows(t *testing.T, p *fixture.Pair, f *fixture.HBAFiles, b fixture.Backend) {
	set := fixture.HBARuleSet{
		Contents: "hostssl all all all trust\nlocal all all radius radiusservers=127.0.0.1 radiussecrets=s radiusports=0\nlocal all all trust\n",
		Rules: []stuntgres.HBARule{
			{FileName: new("{MAIN}"), LineNumber: new(int32(1)), Type: new("hostssl"), Database: []string{"all"}, UserName: []string{"all"}, Address: new("all"), AuthMethod: new("trust"), Error: new(hbaSSLWarning)},
			{RuleNumber: new(int32(1)), FileName: new("{MAIN}"), LineNumber: new(int32(2))},
			{RuleNumber: new(int32(2)), FileName: new("{MAIN}"), LineNumber: new(int32(3)), Type: new("local"), Database: []string{"all"}, UserName: []string{"all"}, AuthMethod: new("trust")},
		},
	}
	f.Install(t, p.Ctx, b, set)
	warning := hbaValid(0, f.Path(b, ""), 1, "hostssl", hbaArray("all"), hbaArray("all"), "all", nil, "trust")
	warning[0], warning[10] = nil, hbaSSLWarning
	want := [][]any{warning, hbaArtifact(1, f.Path(b, ""), 2), hbaLocal(2, f.Path(b, ""), 3, "trust")}
	for _, prepared := range []bool{false, true} {
		hbaRead(t, p, b, b.DB, hbaSelect+" ORDER BY line_number", want, prepared)
	}
}

// Arbitrary simulated states deliberately do not claim PostgreSQL grammar parity.
// This is a native phase of the shared dual-mode definition, not a second suite.
func hbaArbitraryRows(t *testing.T, p *fixture.Pair, f *fixture.HBAFiles, b fixture.Backend) {
	set := fixture.HBARuleSet{Rules: []stuntgres.HBARule{
		{},
		{RuleNumber: new(int32(0)), FileName: new(""), LineNumber: new(int32(0)), Type: new(""), Database: []string{}, UserName: []string{""}, Address: new(""), Netmask: new(""), AuthMethod: new(""), Options: []string{}, Error: new("")},
		{RuleNumber: new(int32(-7)), Database: []string{"NULL", "a,b", `a\b`}, UserName: []string{}, Options: []string{""}, Error: new("verbatim diagnostic")},
		{RuleNumber: new(int32(-7)), Database: []string{"NULL", "a,b", `a\b`}, UserName: []string{}, Options: []string{""}, Error: new("verbatim diagnostic")},
	}}
	f.Install(t, p.Ctx, b, set)
	want := [][]any{
		{nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil},
		{int64(0), "", int64(0), "", "{}", hbaArray(""), "", "", "", "{}", ""},
		{int64(-7), nil, nil, nil, hbaArray("NULL", "a,b", `a\b`), "{}", nil, nil, nil, hbaArray(""), "verbatim diagnostic"},
		{int64(-7), nil, nil, nil, hbaArray("NULL", "a,b", `a\b`), "{}", nil, nil, nil, hbaArray(""), "verbatim diagnostic"},
	}
	for _, prepared := range []bool{false, true} {
		hbaRead(t, p, b, b.DB, hbaSelect, want, prepared)
	}
}
