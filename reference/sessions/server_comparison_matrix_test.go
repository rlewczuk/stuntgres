//go:build !noservertests

package reference_test

import (
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Every ordered nonnull-decodable source pair and all seven spellings, not a
// provider-family shortlist. Scenario windows are one left source each, keeping
// the existing fixture context and package-owned serialized oracle unchanged.
func TestReferenceServerComparisonWireMatrix(t *testing.T) {
	sources := serverComparisonSources(t)
	visited, admitted := 0, 0
	for li, left := range sources {
		mask, err := hex.DecodeString(serverComparisonAdmission[li])
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range mask {
			for bit := range 7 {
				if v&(1<<bit) != 0 {
					admitted++
				}
			}
		}
		t.Run(left.label(), func(t *testing.T) {
			f := fixture.NewServerFixture(t)
			for _, b := range f.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(b))
					serverWantCode(t, serverCycle(t, w, serverParse("", "SET TimeZone = 'UTC'"), serverBind("", "", nil, nil), serverExecute("", 0), serverClose('S', "")), "")
					serverComparisonWirePath(t, w, "public,pg_catalog")
					serverComparisonWireSource(t, w, left)
					for ri, right := range sources {
						t.Run(right.label(), func(t *testing.T) {
							for oi, op := range []string{"=", "<>", "!=", "<", "<=", ">", ">="} {
								t.Run(op, func(t *testing.T) {
									allow := mask[ri]&(1<<oi) != 0
									serverComparisonWireCell(t, w, left, right, op, allow)
								})
							}
						})
					}
				})
			}
		})
		visited += len(sources) * 7
	}
	if visited != 51772 || admitted != 2447 {
		t.Fatalf("wire coverage cells=%d admitted=%d want51772/2447", visited, admitted)
	}
}

func serverComparisonWireCell(t *testing.T, w *serverWire, left, right serverComparisonSource, op string, allow bool) {
	t.Helper()
	query := "SELECT $1 AS l,$2 AS r,$1 " + op + " $2 AS v"
	position := strconv.Itoa(strings.Index(query, " "+op+" ") + 2)
	canonical := op
	if op == "!=" {
		canonical = "<>"
	}
	oids := []uint32{left.oid, right.oid}
	for _, name := range []string{"", "wire_matrix"} {
		frames := serverCycle(t, w, serverParse(name, query, oids...), serverDescribe('S', name))
		if !allow {
			serverComparisonError(t, frames, "42883", "operator does not exist: "+left.diagnostic+" "+canonical+" "+right.diagnostic, position, "No operator matches the given name and argument types. You might need to add explicit type casts.")
			if name != "" {
				serverNotificationError(t, serverCycle(t, w, serverDescribe('S', name)), "26000", "prepared statement \""+name+"\" does not exist", "")
			}
			// Analysis rejection must retain source diagnostics when the effective
			// lookup path changes, rather than depending on a prior failed handle.
			serverComparisonWirePath(t, w, "pg_catalog,public")
			serverComparisonError(t, serverCycle(t, w, serverParse(name, query, oids...)), "42883", "operator does not exist: "+left.diagnostic+" "+canonical+" "+right.diagnostic, position, "No operator matches the given name and argument types. You might need to add explicit type casts.")
			if name != "" {
				serverNotificationError(t, serverCycle(t, w, serverDescribe('S', name)), "26000", "prepared statement \""+name+"\" does not exist", "")
			}
			serverComparisonWirePath(t, w, "public,pg_catalog")
			continue
		}
		serverComparisonDescription(t, frames, oids, []uint32{left.oid, right.oid, 16})
		demand := serverComparisonWireDemand(left, right, op)
		// All base witnesses are equal under their released selected formals.
		// Independent alternates test both directions, then survive NULL/reuse.
		lowLeft, lowRight := left.fresh(t).low, right.fresh(t).low
		highLeft, highRight := left.fresh(t).high, right.fresh(t).high
		leftSign, rightSign := 1, -1
		if left.oid == 16 || left.oid == 1000 {
			leftSign = -1
		}
		if right.oid == 16 || right.oid == 1000 {
			rightSign = 1
		}
		for lfi, lf := range left.formats() {
			for rfi, rf := range right.formats() {
				l, r := left.input(t, lf), right.input(t, rf)
				al, ar := left.alternate(t, lf), right.alternate(t, rf)
				for si, sample := range []struct {
					args                [][]byte
					wantLeft, wantRight string
					sign                int
				}{
					{[][]byte{l, r}, lowLeft, lowRight, 0},
					{[][]byte{al, r}, highLeft, lowRight, leftSign},
					{[][]byte{l, ar}, lowLeft, highRight, rightSign},
					{[][]byte{nil, r}, "", lowRight, 0},
					{[][]byte{l, nil}, lowLeft, "", 0},
					{[][]byte{nil, nil}, "", "", 0},
					{[][]byte{al, nil}, highLeft, "", 0},
					{[][]byte{nil, ar}, "", highRight, 0},
					{[][]byte{al, r}, highLeft, lowRight, leftSign},
					{[][]byte{l, r}, lowLeft, lowRight, 0},
				} {
					if si == 1 && lfi == 0 && rfi == 0 {
						// After a real base execution, force compatible effective-path
						// reanalysis before fresh Bind without reparsing this statement.
						serverComparisonWirePath(t, w, "pg_catalog,public")
						serverComparisonDescription(t, serverCycle(t, w, serverDescribe('S', name)), oids, []uint32{left.oid, right.oid, 16})
					}
					args := sample.args
					frames = serverCycle(t, w, serverBind("", name, []int{lf, rf}, args), serverDescribe('P', ""), serverExecute("", 0))
					if demand != "" && args[0] != nil && args[1] != nil {
						serverComparisonError(t, frames, "42883", demand, "", "")
						continue
					}
					serverWantCode(t, frames, "")
					columns := serverColumns(t, frames)
					if len(columns) != 3 || columns[0].oid != left.oid || columns[1].oid != right.oid || columns[2].oid != 16 {
						t.Fatalf("source/result identities %+v", columns)
					}
					rows := serverRawRows(t, frames)
					if len(rows) != 1 || len(rows[0]) != 3 {
						t.Fatalf("comparison rows %x", rows)
					}
					for i, want := range []string{sample.wantLeft, sample.wantRight} {
						if (rows[0][i] == nil) != (args[i] == nil) || args[i] != nil && string(rows[0][i]) != want {
							t.Fatalf("source%d output %q want%q (input%x)", i, rows[0][i], want, args[i])
						}
					}
					if args[0] == nil || args[1] == nil {
						if rows[0][2] != nil {
							t.Fatalf("strict result %x wantNULL", rows)
						}
					} else if want := serverComparisonWireTruth(op, sample.sign); string(rows[0][2]) != want {
						t.Fatalf("comparison result %q want%s (relation%d)", rows[0][2], want, sample.sign)
					}
				}
			}
		}
		serverComparisonWirePath(t, w, "public,pg_catalog")
		serverComparisonDescription(t, serverCycle(t, w, serverDescribe('S', name)), oids, []uint32{left.oid, right.oid, 16})
		serverWantCode(t, serverCycle(t, w, serverClose('S', name)), "")
	}
	// The same original descriptors must be available on an empty result, before
	// Bind and after successful execution; emptiness cannot decide admission.
	if allow {
		empty := query + " WHERE false"
		serverComparisonDescription(t, serverCycle(t, w, serverParse("", empty, oids...), serverDescribe('S', "")), oids, []uint32{left.oid, right.oid, 16})
		frames := serverCycle(t, w, serverBind("", "", []int{left.formats()[0], right.formats()[0]}, [][]byte{nil, nil}), serverDescribe('P', ""), serverExecute("", 0))
		serverWantCode(t, frames, "")
		if len(serverRawRows(t, frames)) != 0 {
			t.Fatal("empty comparison produced rows")
		}
		serverWantCode(t, serverCycle(t, w, serverClose('S', "")), "")
	}
}

// Extended named SET/portal traffic preserves even the unnamed comparison
// statement. A simple-query SET would silently destroy that PostgreSQL handle.
func serverComparisonWirePath(t *testing.T, w *serverWire, path string) {
	t.Helper()
	serverWantCode(t, serverCycle(t, w,
		serverParse("wire_path_set", "SET search_path = "+path),
		serverBind("wire_path_portal", "wire_path_set", nil, nil),
		serverExecute("wire_path_portal", 0),
		serverClose('P', "wire_path_portal"),
		serverClose('S', "wire_path_set")), "")
}

func serverComparisonWireDemand(left, right serverComparisonSource, op string) string {
	if left.oid != right.oid {
		return ""
	}
	leaf := ""
	switch left.oid {
	case 199:
		leaf = "json"
	case 4073:
		leaf = "jsonpath"
	case 1034:
		if op == "=" || op == "<>" || op == "!=" {
			return ""
		}
		leaf = "aclitem"
	}
	if leaf == "" {
		return ""
	}
	if op == "=" || op == "<>" || op == "!=" {
		return "could not identify an equality operator for type " + leaf
	}
	return "could not identify a comparison function for type " + leaf
}

func serverComparisonWireSource(t *testing.T, w *serverWire, source serverComparisonSource) {
	t.Helper()
	serverComparisonDescription(t, serverCycle(t, w, serverParse("source", "SELECT $1 AS v,$1 IS NULL", source.oid), serverDescribe('S', "source")), []uint32{source.oid}, []uint32{source.oid, 16})
	fact := source.fresh(t)
	for _, format := range source.formats() {
		samples := []struct {
			input []byte
			want  string
		}{{source.input(t, format), fact.low}, {nil, ""}, {source.input(t, format), fact.low}}
		if source.oid != 2278 {
			samples = append(samples[:1], []struct {
				input []byte
				want  string
			}{{source.alternate(t, format), fact.high}, {nil, ""}, {source.alternate(t, format), fact.high}, {source.input(t, format), fact.low}}...)
		}
		for _, sample := range samples {
			input := sample.input
			frames := serverCycle(t, w, serverBind("", "source", []int{format}, [][]byte{input}), serverExecute("", 0))
			serverWantCode(t, frames, "")
			rows := serverRawRows(t, frames)
			if len(rows) != 1 || len(rows[0]) != 2 {
				t.Fatalf("source rows %x", rows)
			}
			want := "f"
			if input == nil {
				want = "t"
			}
			if (rows[0][0] == nil) != (input == nil) || input != nil && string(rows[0][0]) != sample.want || string(rows[0][1]) != want {
				t.Fatalf("source output %q want%q NULL=%s input%x", rows[0][0], sample.want, want, input)
			}
		}
	}
	serverComparisonDescription(t, serverCycle(t, w, serverDescribe('S', "source")), []uint32{source.oid}, []uint32{source.oid, 16})
	serverWantCode(t, serverCycle(t, w, serverClose('S', "source")), "")
}
