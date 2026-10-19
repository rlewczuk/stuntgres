//go:build !noservertests

package reference_test

import (
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var serverComparisonOperators = []string{"=", "<>", "!=", "<", "<=", ">", ">="}

// Every ordered nonnull-decodable source pair and all seven spellings, not a
// provider-family shortlist. Every cell asserts its own admission: the complete
// 42883 diagnostic when denied, or the descriptions and value semantics when
// admitted. Statement-lifecycle repeats, which do not depend on the spelling,
// run once per ordered pair: the named statement and its absence after a failed
// Parse, and diagnostics after an effective search_path change. One server
// fixture serves the whole matrix; each left source gets a fresh session.
func TestReferenceServerComparisonWireMatrix(t *testing.T) {
	sources := serverComparisonSources(t)
	masks := make([][]byte, len(sources))
	visited, admitted := 0, 0
	for li := range sources {
		mask, err := hex.DecodeString(serverComparisonAdmission[li])
		if err != nil {
			t.Fatal(err)
		}
		if len(mask) != len(sources) {
			t.Fatalf("admission row %d has %d sources, want %d", li, len(mask), len(sources))
		}
		masks[li] = mask
		for _, v := range mask {
			for bit := range len(serverComparisonOperators) {
				if v&(1<<bit) != 0 {
					admitted++
				}
			}
		}
		visited += len(sources) * len(serverComparisonOperators)
	}
	if visited != 51772 || admitted != 2447 {
		t.Fatalf("wire coverage cells=%d admitted=%d want51772/2447", visited, admitted)
	}
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for li, left := range sources {
				t.Run(left.label(), func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(b))
					serverWantCode(t, serverCycle(t, w, serverParse("", "SET TimeZone = 'UTC'"), serverBind("", "", nil, nil), serverExecute("", 0), serverClose('S', "")), "")
					serverComparisonWirePath(t, w, "public,pg_catalog")
					serverComparisonWireSource(t, w, left)
					for ri, right := range sources {
						t.Run(right.label(), func(t *testing.T) {
							// The session spans every right source; renew its I/O deadline per pair.
							if err := w.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
								t.Fatal(err)
							}
							var denied, allowed []string
							for oi, op := range serverComparisonOperators {
								if masks[li][ri]&(1<<oi) != 0 {
									allowed = append(allowed, op)
								} else {
									denied = append(denied, op)
								}
							}
							serverComparisonWireDenied(t, w, left, right, denied)
							for i, op := range allowed {
								t.Run(op, func(t *testing.T) {
									names := []string{""}
									if i == 0 {
										names = append(names, "wire_matrix")
									}
									serverComparisonWireAdmitted(t, w, left, right, op, names)
								})
							}
						})
					}
				})
			}
		})
	}
}

func serverComparisonWireQuery(op string) string {
	return "SELECT $1 AS l,$2 AS r,$1 " + op + " $2 AS v"
}

// serverComparisonWireDenied pipelines the pair's denied spellings. Each one
// must fail its unnamed Parse with the complete diagnostic. The first one also
// proves the lifecycle: a failed named Parse leaves no handle, and the same
// diagnostics follow an effective search_path change instead of depending on
// a prior failed handle.
func serverComparisonWireDenied(t *testing.T, w *serverWire, left, right serverComparisonSource, ops []string) {
	t.Helper()
	if len(ops) == 0 {
		return
	}
	oids := []uint32{left.oid, right.oid}
	var steps []serverStep
	add := func(check func([]serverFrame), packets ...[]byte) {
		steps = append(steps, serverStep{packets, check})
	}
	parse := func(op, name string, describe bool) {
		packets := [][]byte{serverParse(name, serverComparisonWireQuery(op), oids...)}
		if describe {
			packets = append(packets, serverDescribe('S', name))
		}
		add(func(frames []serverFrame) { serverComparisonWireRejected(t, frames, left, right, op, name) }, packets...)
	}
	missing := func(frames []serverFrame) {
		serverNotificationError(t, frames, "26000", "prepared statement \"wire_matrix\" does not exist", "")
	}
	succeeded := func(frames []serverFrame) { serverWantCode(t, frames, "") }
	for _, op := range ops {
		parse(op, "", true)
	}
	parse(ops[0], "wire_matrix", true)
	add(missing, serverDescribe('S', "wire_matrix"))
	add(succeeded, serverComparisonWirePathPackets("pg_catalog,public")...)
	parse(ops[0], "", false)
	parse(ops[0], "wire_matrix", false)
	add(missing, serverDescribe('S', "wire_matrix"))
	add(succeeded, serverComparisonWirePathPackets("public,pg_catalog")...)
	serverSteps(t, w, steps)
}

func serverComparisonWireRejected(t *testing.T, frames []serverFrame, left, right serverComparisonSource, op, name string) {
	t.Helper()
	defer func() {
		if t.Failed() {
			t.Logf("operator %s, statement %q", op, name)
		}
	}()
	query := serverComparisonWireQuery(op)
	position := strconv.Itoa(strings.Index(query, " "+op+" ") + 2)
	canonical := op
	if op == "!=" {
		canonical = "<>"
	}
	serverComparisonError(t, frames, "42883", "operator does not exist: "+left.diagnostic+" "+canonical+" "+right.diagnostic, position, "No operator matches the given name and argument types. You might need to add explicit type casts.")
}

// serverComparisonWireAdmitted pipelines one admitted cell under each named
// statement: descriptions, every input-format pair with base, alternate, NULL
// and reused values, compatible re-analysis before a fresh Bind, and finally
// the descriptions of an empty result.
func serverComparisonWireAdmitted(t *testing.T, w *serverWire, left, right serverComparisonSource, op string, names []string) {
	t.Helper()
	query := serverComparisonWireQuery(op)
	oids := []uint32{left.oid, right.oid}
	results := []uint32{left.oid, right.oid, 16}
	var steps []serverStep
	add := func(check func([]serverFrame), packets ...[]byte) {
		steps = append(steps, serverStep{packets, check})
	}
	described := func(frames []serverFrame) { serverComparisonDescription(t, frames, oids, results) }
	succeeded := func(frames []serverFrame) { serverWantCode(t, frames, "") }
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
	for _, name := range names {
		add(described, serverParse(name, query, oids...), serverDescribe('S', name))
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
						add(succeeded, serverComparisonWirePathPackets("pg_catalog,public")...)
						add(described, serverDescribe('S', name))
					}
					args := sample.args
					add(func(frames []serverFrame) {
						if demand != "" && args[0] != nil && args[1] != nil {
							serverComparisonError(t, frames, "42883", demand, "", "")
							return
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
								t.Fatalf("statement %q source%d output %q want%q (input%x)", name, i, rows[0][i], want, args[i])
							}
						}
						if args[0] == nil || args[1] == nil {
							if rows[0][2] != nil {
								t.Fatalf("statement %q strict result %x wantNULL", name, rows)
							}
						} else if want := serverComparisonWireTruth(op, sample.sign); string(rows[0][2]) != want {
							t.Fatalf("statement %q comparison result %q want%s (relation%d)", name, rows[0][2], want, sample.sign)
						}
					}, serverBind("", name, []int{lf, rf}, args), serverDescribe('P', ""), serverExecute("", 0))
				}
			}
		}
		add(succeeded, serverComparisonWirePathPackets("public,pg_catalog")...)
		add(described, serverDescribe('S', name))
		add(succeeded, serverClose('S', name))
	}
	// The same original descriptors must be available on an empty result, before
	// Bind and after successful execution; emptiness cannot decide admission.
	add(described, serverParse("", query+" WHERE false", oids...), serverDescribe('S', ""))
	add(func(frames []serverFrame) {
		serverWantCode(t, frames, "")
		if len(serverRawRows(t, frames)) != 0 {
			t.Fatal("empty comparison produced rows")
		}
	}, serverBind("", "", []int{left.formats()[0], right.formats()[0]}, [][]byte{nil, nil}), serverDescribe('P', ""), serverExecute("", 0))
	add(succeeded, serverClose('S', ""))
	serverSteps(t, w, steps)
}

// Extended named SET/portal traffic preserves even the unnamed comparison
// statement. A simple-query SET would silently destroy that PostgreSQL handle.
func serverComparisonWirePath(t *testing.T, w *serverWire, path string) {
	t.Helper()
	serverWantCode(t, serverCycle(t, w, serverComparisonWirePathPackets(path)...), "")
}

func serverComparisonWirePathPackets(path string) [][]byte {
	return [][]byte{
		serverParse("wire_path_set", "SET search_path = "+path),
		serverBind("wire_path_portal", "wire_path_set", nil, nil),
		serverExecute("wire_path_portal", 0),
		serverClose('P', "wire_path_portal"),
		serverClose('S', "wire_path_set"),
	}
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
