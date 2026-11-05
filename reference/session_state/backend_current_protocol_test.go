package reference_test

import (
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"stuntgres/reference/fixture"
	"testing"
)

func backendCurrentProtocol(t *testing.T) {
	t.Helper()
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run("wire/"+b.Name, func(t *testing.T) {
			c, err := pgconn.Connect(f.Ctx, f.DSNFor(b))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close(f.Ctx)
			if deadline, ok := f.Ctx.Deadline(); ok {
				if err := c.Conn().SetDeadline(deadline); err != nil {
					t.Fatal(err)
				}
			}
			ioIDs := make([]uint32, len(statIOOIDs))
			for i, id := range statIOOIDs {
				ioIDs[i] = uint32(id)
			}
			ids := [][]uint32{backendActivityOIDs, ioIDs, backendWALOIDs}
			for kind, name := range backendFunctionNames {
				for _, scalar := range []bool{false, true} {
					q := "SELECT * FROM " + name + "(0) LIMIT 0"
					names, oids := backendOutputNames[kind], ids[kind]
					if scalar {
						q = "SELECT " + name + "(0) AS value LIMIT 0"
						names = []string{"value"}
						oids = []uint32{2249}
					}
					d, err := c.Prepare(f.Ctx, "", q, nil)
					if err != nil {
						t.Fatal(err)
					}
					backendWireFields(t, d.Fields, names, oids)
					c.Frontend().Send(&pgproto3.Bind{})
					c.Frontend().Send(&pgproto3.Describe{ObjectType: 'P'})
					c.Frontend().Send(&pgproto3.Execute{})
					c.Frontend().Send(&pgproto3.Sync{})
					if err := c.Frontend().Flush(); err != nil {
						t.Fatal(err)
					}
					bound, described, complete := false, false, false
					for {
						m, err := c.ReceiveMessage(f.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						switch m := m.(type) {
						case *pgproto3.BindComplete:
							if bound || described {
								t.Fatal("duplicate bind")
							}
							bound = true
						case *pgproto3.RowDescription:
							if !bound || described {
								t.Fatal("portal describe order")
							}
							described = true
							fields := make([]pgconn.FieldDescription, len(m.Fields))
							for i, x := range m.Fields {
								fields[i] = pgconn.FieldDescription{Name: string(x.Name), TableOID: x.TableOID, TableAttributeNumber: x.TableAttributeNumber, DataTypeOID: x.DataTypeOID, DataTypeSize: x.DataTypeSize, TypeModifier: x.TypeModifier, Format: x.Format}
							}
							backendWireFields(t, fields, names, oids)
						case *pgproto3.CommandComplete:
							if !described || complete || string(m.CommandTag) != "SELECT 0" {
								t.Fatal("empty source completion", m)
							}
							complete = true
						case *pgproto3.ReadyForQuery:
							if !complete || m.TxStatus != 'I' {
								t.Fatal("incomplete portal", m)
							}
							goto next
						default:
							t.Fatalf("unexpected portal message %T %#v", m, m)
						}
					}
				next:
				}
			}
		})
	}
}
func backendWireFields(t *testing.T, fields []pgconn.FieldDescription, names []string, ids []uint32) {
	t.Helper()
	if len(fields) != len(ids) {
		t.Fatal("wire fixed shape", fields, ids)
	}
	for i, f := range fields {
		size := int16(-1)
		switch ids[i] {
		case 16:
			size = 1
		case 23, 26, 28:
			size = 4
		case 20, 701, 1184:
			size = 8
		}
		if f.Name != names[i] || f.DataTypeOID != ids[i] || f.DataTypeSize != size || f.TypeModifier != -1 || f.TableOID != 0 || f.TableAttributeNumber != 0 || f.Format != 0 {
			t.Fatal("computed wire descriptor", i, f, names[i], ids[i], size)
		}
	}
}
