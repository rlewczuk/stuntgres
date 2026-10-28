package core_test

import (
	"context"
	"errors"
	"fmt"
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestXMLSQLPreparedPortalOwnership(t *testing.T) {
	for _, oid := range []uint32{25, 142} {
		for _, between := range []bool{false, true} {
			t.Run(fmt.Sprintf("oid%d/between%t", oid, between), func(t *testing.T) {
				e := rootengine.New()
				s := enginetest.StartSession(t, e, nil)
				transaction := enginetest.TransactionBegin(t, s)
				defer enginetest.TransactionEnd(t, s, transaction, false)
				cycle := s.NewProtocolCycle()
				command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
				command(func(scope *core.StatementScope) error {
					_, err := scope.ParseProtocol("xml_modes", `SELECT XML 'text<a/>' AS frozen,$1::xml AS live,length('owned') AS folded FROM (VALUES(1),(2)) AS input(n)`, []uint32{oid})
					return err
				})
				packet := append([]byte{0, 0, 0, 1, 0, 0, 0, 8}, []byte("text<a/>")...)
				packet = append(packet, 0, 0)
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal("xml_modes_portal", "xml_modes", &protocolUnitInput{data: packet})
					return err
				})
				registry := core.PayloadOf(s).PreparedSQL
				definition := registry.Lookup("xml_modes")
				binding := registry.Binding("xml_modes_portal")
				if binding == nil || binding.Resource == nil || binding.Resource.Root == nil || binding.Folds == nil {
					t.Fatal("missing actual accepted portal")
				}
				graph, accepted := binding.Resource.Root, *binding.Folds
				literal := xmlPreparedLiteral(t, definition.Description)
				graphLiteral := xmlGraphLiteral(t, graph)
				if !sqltype.SameDatum(literal, graphLiteral) || len(accepted.Entries) == 0 {
					t.Fatal("missing accepted input/fold backing")
				}
				first, generation := &accepted.Entries[0], accepted.Generation
				execute := func() (int, error) {
					op, _, err := s.AcquireCommand(context.Background())
					if err != nil {
						return 0, err
					}
					scope, err := e.StartProtocolCommand(op, cycle)
					if err != nil {
						op.Done()
						return 0, err
					}
					rows := 0
					_, err = scope.ExecuteProtocolPortal("xml_modes_portal", 1, func(fields []session.ProtocolField) error {
						if len(fields) != 3 || string(fields[0].Bytes) != "text<a/>" || string(fields[1].Bytes) != "text<a/>" || string(fields[2].Bytes) != "5" {
							t.Error("actual accepted portal row", fields)
						}
						rows++
						return nil
					}, nil, nil, core.CopyProtocolHooks{})
					err = scope.Finish(err)
					op.Done()
					return rows, err
				}
				if between {
					if rows, err := execute(); err != nil || rows != 1 {
						t.Fatal("initial admitted CONTENT row", rows, err)
					}
				}
				xmlPreparedMode(t, e, s, "document")
				rows, err := execute()
				if oid == 25 {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "2200M" || diagnostic.Context != "" || rows != 0 {
						t.Fatal("live TEXT conversion did not recapture mode", rows, err)
					}
					if binding.Resource.Root != nil {
						t.Fatal("failed live producer did not detach")
					}
				} else if err != nil || rows != 1 {
					t.Fatal("accepted XML parameter was reparsed", rows, err)
				}
				if !sqltype.SameDatum(graphLiteral, xmlGraphLiteral(t, graph)) || !sqltype.SameDatum(literal, xmlPreparedLiteral(t, definition.Description)) || accepted.Generation != generation || &accepted.Entries[0] != first {
					t.Fatal("admission changed accepted graph/group/backing")
				}
			})
		}
	}
}
