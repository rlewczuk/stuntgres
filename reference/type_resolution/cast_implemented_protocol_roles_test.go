package reference_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"stuntgres/reference/fixture"
)

// REL_18_6 array header/dimensions/NULL length, xid uint32 and enum label bytes.
func castRoleBinaryArray(element uint32, member []byte) []byte {
	var data []byte
	for _, n := range []uint32{1, 1, element, 2, 0, uint32(len(member))} {
		data = binary.BigEndian.AppendUint32(data, n)
	}
	data = append(data, member...)
	return binary.BigEndian.AppendUint32(data, 0xffffffff)
}
func castRoleImportError(name string, format int16, pg bool) *fixture.Diagnostic {
	var d *fixture.Diagnostic
	switch name {
	case "pg_node_tree":
		d = &fixture.Diagnostic{Code: "0A000", Message: "cannot accept a value of type pg_node_tree"}
	case "cast_deferred_record", "record[]":
		message := "input of anonymous composite types is not implemented"
		if !pg && format == 1 {
			message = "binary input is not supported for type record"
		}
		d = &fixture.Diagnostic{Code: "0A000", Message: message}
	case "void":
		if !pg && format == 0 {
			d = &fixture.Diagnostic{Code: "0A000", Message: "input of type void is not supported"}
		}
	}
	if d != nil {
		d.Context = "unnamed portal parameter $1"
		if format == 0 {
			d.Context += " = '...'"
		}
	}
	return d
}
func TestReferenceCastImplementedProtocolRoles(t *testing.T) {
	// One listener/userfile/setup/connection per backend, not per role or pair.
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			castInventorySetup(t, f.Ctx, b)
			conn, err := pgconn.Connect(f.Ctx, f.DSNFor(b))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close(f.Ctx)
			oids := map[string]uint32{}
			oidFor := func(t *testing.T, fact castImplementedType) uint32 {
				t.Helper()
				if oid, ok := oids[fact.name]; ok {
					return oid
				}
				oid := castProtocolOID(t, f, b, fact)
				oids[fact.name] = oid
				return oid
			}
			execute := func(t *testing.T, statement string, oid uint32, format int16, arg []byte, want string, diagnostic *fixture.Diagnostic) {
				t.Helper()
				rr := conn.ExecPrepared(f.Ctx, statement, [][]byte{arg}, []int16{format}, []int16{0})
				got := rr.Read()
				if diagnostic != nil {
					castProtocolError(t, got.Err, diagnostic)
					return
				}
				if got.Err != nil {
					t.Fatal(got.Err)
				}
				castProtocolFields(t, rr.FieldDescriptions(), oid, -1)
				if len(got.Rows) != 1 || len(got.Rows[0]) != 1 {
					t.Fatalf("role rows=%v", got.Rows)
				}
				if arg == nil {
					if got.Rows[0][0] != nil {
						t.Fatalf("NULL became %q", got.Rows[0][0])
					}
				} else if got.Rows[0][0] == nil || !bytes.Equal(got.Rows[0][0], []byte(want)) {
					t.Fatalf("role value=%q want %q", got.Rows[0][0], want)
				}
			}
			for roleIndex, name := range []string{`"char"`, "pg_node_tree", "void", "cast_deferred_record", "record[]", "xid[]", "cast_enum_a[]", "cast_enum_b[]"} {
				t.Run(name, func(t *testing.T) {
					source := castInventoryType(name)
					oid := oidFor(t, source)
					textInput, output := source.seed, source.output
					var binaryInput, changedBinary []byte
					changedInput, changedOutput := "", ""
					switch name {
					case `"char"`:
						binaryInput = []byte{'r'}
						changedInput, changedOutput = "s", "s"
						changedBinary = []byte{'s'}
					case "pg_node_tree":
						textInput = "ignored"
						binaryInput = []byte{'x'}
					case "void":
						textInput = "ignored"
						binaryInput = []byte{}
					case "cast_deferred_record":
						textInput = "(1,red)"
						binaryInput = binary.BigEndian.AppendUint32(nil, 0)
					case "record[]":
						binaryInput = castRoleBinaryArray(2249, binary.BigEndian.AppendUint32(nil, 0))
					case "xid[]":
						textInput, output = "[0:1]={1,NULL}", "[0:1]={1,NULL}"
						binaryInput = castRoleBinaryArray(28, binary.BigEndian.AppendUint32(nil, 1))
						changedInput, changedOutput = "[0:1]={2,NULL}", "[0:1]={2,NULL}"
						changedBinary = castRoleBinaryArray(28, binary.BigEndian.AppendUint32(nil, 2))
					default:
						element := oidFor(t, castInventoryType(source.element))
						textInput, output = "[0:1]={red,NULL}", "[0:1]={red,NULL}"
						binaryInput = castRoleBinaryArray(element, []byte("red"))
						changedInput, changedOutput = "[0:1]={blue,NULL}", "[0:1]={blue,NULL}"
						changedBinary = castRoleBinaryArray(element, []byte("blue"))
					}
					for _, format := range []int16{0, 1} {
						statement := fmt.Sprintf("role_%d_%d", roleIndex, format)
						d, e := conn.Prepare(f.Ctx, statement, "SELECT $1 AS v", []uint32{oid})
						if name == "record[]" && !b.PG {
							castProtocolError(t, e, &fixture.Diagnostic{Code: "0A000", Message: "type record[] is not supported"})
							continue
						}
						if e != nil {
							t.Fatal(e)
						}
						if len(d.ParamOIDs) != 1 || d.ParamOIDs[0] != oid {
							t.Fatal("supplied OID changed", d.ParamOIDs)
						}
						castProtocolFields(t, d.Fields, oid, -1)
						input := []byte(textInput)
						if format == 1 {
							input = binaryInput
						}
						execute(t, statement, oid, format, input, output, castRoleImportError(name, format, b.PG))
						execute(t, statement, oid, format, nil, "", nil)
						if changedInput != "" {
							input = []byte(changedInput)
							if format == 1 {
								input = changedBinary
							}
							execute(t, statement, oid, format, input, changedOutput, nil)
						}
					}
				})
			}
			for i, pair := range [][2]string{{`"char"`, "int4"}, {"xid[]", "text"}, {"cast_enum_a[]", "cast_enum_a[]"}, {"cast_enum_b[]", "cast_enum_b[]"}} {
				cell := castCaseCell(pair[0], pair[1])
				value := castScalarMatrixValue(cell)
				oid := oidFor(t, cell.source)
				resultOID := oidFor(t, castMatrixResultFact(cell))
				q := "SELECT CAST($1 AS " + castInventoryIdentity(cell.target) + ") AS v"
				name := fmt.Sprintf("role_cast_%d", i)
				d, e := conn.Prepare(f.Ctx, name, q, []uint32{oid})
				if e != nil {
					t.Fatal(e)
				}
				castProtocolFields(t, d.Fields, resultOID, -1)
				execute(t, name, resultOID, 0, []byte(value.input), value.output, nil)
				execute(t, name, resultOID, 0, nil, "", nil)
			}
			// VOID Param elision changes the actual call arity, not CAST semantics.
			for i, target := range []string{"int4", "int4multirange", "cast_multirange", "earth"} {
				cell, value, diagnostic := castProtocolFunctionCell(castCaseCell("void", target))
				q := "SELECT " + castFunctionName(cell.target) + "($1) AS v"
				name := fmt.Sprintf("void_call_%d", i)
				d, e := conn.Prepare(f.Ctx, name, q, []uint32{2278})
				if diagnostic != nil {
					castProtocolError(t, e, diagnostic)
					continue
				}
				if e != nil {
					t.Fatal(e)
				}
				oid := oidFor(t, castMatrixResultFact(cell))
				castProtocolFields(t, d.Fields, oid, -1)
				execute(t, name, oid, 0, []byte("ignored"), value.output, castRoleImportError("void", 0, b.PG))
				rr := conn.ExecPrepared(f.Ctx, name, [][]byte{nil}, []int16{0}, []int16{0})
				got := rr.Read()
				if got.Err != nil {
					t.Fatal(got.Err)
				}
				castProtocolFields(t, rr.FieldDescriptions(), oid, -1)
				if len(got.Rows) != 1 || len(got.Rows[0]) != 1 || got.Rows[0][0] == nil || string(got.Rows[0][0]) != value.output {
					t.Fatalf("zeroarg NULL-param result=%v want %q", got.Rows, value.output)
				}
			}
		})
	}
}
