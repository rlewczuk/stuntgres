package core_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/workadmission"
)

func TestSepgsqlSimulationCodecTransport(t *testing.T) {
	for _, change := range []string{"relabel", "descriptor"} {
		t.Run("encoded_copy_pre_sink_"+change, func(t *testing.T) {
			e, _ := sepgsqlCodecEngine(t, "S")
			enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean) IS 'S'")
			ctx, cancel := context.WithTimeout(context.Background(), enginetest.Liveness)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var enterOnce, releaseOnce sync.Once
			ctx = workadmission.With(ctx, func(_ context.Context, class workadmission.Class) error {
				if class == workadmission.WorkEncodingConvert {
					enterOnce.Do(func() { close(entered); <-release })
				}
				return nil
			})
			op, done, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			enginetest.RoleExecute(t, e, op, "SET client_encoding='LATIN1'")
			enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			core.SetProtocolScopeCommand(core.ScopeProtocol(scope), true)
			statement := enginetest.Statement(t, "COPY (SELECT repeat('é',8192)) TO STDOUT").(*parser.Copy)
			var output bytes.Buffer
			finished := make(chan error, 1)
			go func() {
				_, err := core.ScopeCopyProtocol(scope, statement, nil, &output, core.CopyProtocolHooks{})
				finished <- err
			}()
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			joined := false
			defer func() {
				unblock()
				if !joined {
					<-finished
				}
			}()
			select {
			case <-entered:
			case err := <-finished:
				joined = true
				t.Fatalf("actual converter did not reach work boundary: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if op.Session.Sepgsql.Current() != "T" {
				t.Fatal("conversion barrier is not inside actual trusted kernel")
			}
			if change == "relabel" {
				enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean) IS 'U'")
			} else {
				enginetest.Run(t, e, "ALTER FUNCTION pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean) RENAME TO sepgsql_copy_stale_converter")
			}
			unblock()
			err = <-finished
			joined = true
			if err == nil || output.Len() != 0 || op.Session.Sepgsql.Current() != "R" {
				t.Fatal("stale encoded COPY datum reached sink or leaked subject", err, output.Len())
			}
			scope.Finish(err)
		})
	}
	for _, change := range []string{"unchanged", "relabel", "descriptor"} {
		t.Run("finalized_simple_returning_pre_sink_"+change, func(t *testing.T) {
			e, typ := sepgsqlCodecEngine(t, "S")
			const converter = "pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean)"
			enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION "+converter+" IS 'S'")
			enginetest.Run(t, e, "CREATE TABLE sepgsql_finalized_codec(v hstore)")
			ctx, cancel := context.WithTimeout(context.Background(), enginetest.Liveness)
			defer cancel()
			op, done, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			enginetest.RoleExecute(t, e, op, "SET client_encoding='LATIN1'")
			enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
			subject, generation, frame, depth := op.Session.Sepgsql.PhaseSnapshot()
			if subject != "R" || frame != 0 || depth != 0 {
				t.Fatal("unexpected caller subject", subject, generation, frame, depth)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			scopeFinished := false
			defer func() {
				if !scopeFinished {
					scope.Finish(err)
				}
			}()
			core.SetProtocolScopeCommand(core.ScopeProtocol(scope), true)
			entered, release := make(chan struct{}), make(chan struct{})
			var enterOnce, releaseOnce sync.Once
			grants := 0
			original := op.Ctx
			defer func() { op.Ctx = original }()
			op.Ctx = workadmission.With(original, func(ctx context.Context, class workadmission.Class) error {
				if class == workadmission.WorkEncodingConvert {
					grants++
					enterOnce.Do(func() {
						close(entered)
						select {
						case <-release:
						case <-ctx.Done():
						}
					})
					return ctx.Err()
				}
				return nil
			})
			// INSERT owns a real finalized store, unlike an ordinary SELECT's
			// live portal. Its installed hstore output then borrows the actual
			// UTF8-to-Latin1 conversion, whose entered kernel supplies the barrier.
			statement := enginetest.Statement(t, "INSERT INTO sepgsql_finalized_codec VALUES ('k=>é'::hstore) RETURNING v,NULL::text AS absent")
			var stored *exec.FinalRows
			descriptions, deliveries := 0, 0
			describe := func(columns []catalog.Column) error {
				descriptions++
				if !reflect.ValueOf(core.ScopeProtocol(scope)).Elem().FieldByName("simplePortal").IsNil() ||
					scope.InsertReturning == nil || scope.InsertReturning.Rows.Count != 1 {
					return errors.New("INSERT did not take the actual finalized simple-result branch")
				}
				stored = scope.InsertReturning.Rows
				if stored.Closed || len(stored.Values) != 2 || stored.Values[0].Type() != typ ||
					stored.Values[0].IsNull() || !stored.Values[1].IsNull() ||
					len(columns) != 2 || columns[0].Name != "v" || columns[0].Usage.Type != typ ||
					columns[1].Name != "absent" || columns[1].Usage.Type.ID() != 25 {
					return errors.New("finalized INSERT store or metadata changed")
				}
				// The request attached above is deliberately inert during SQL
				// binding. This real unlocked output frontier has now admitted
				// the INSERT scope and finalized its store. Activate that same
				// scope's runtime on the operation context actually consumed by
				// EncodeProtocolRow, preserving cancellation and owner fences.
				op.Ctx = scope.ActivateRuntime(op.Context())
				return nil
			}
			row := func(fields []session.ProtocolField) error {
				deliveries++
				if len(fields) != 2 || fields[0].Null || !bytes.Equal(fields[0].Bytes, []byte("\"k\"=>\"\xe9\"")) ||
					!fields[1].Null || len(fields[1].Bytes) != 0 {
					return errors.New("finalized INSERT did not deliver the exact Latin1 fields and SQL NULL")
				}
				return nil
			}
			var result core.Result
			finished := make(chan error, 1)
			go func() {
				var executeErr error
				result, executeErr = scope.ExecuteProtocolSimple(statement, describe, row, nil, nil, core.CopyProtocolHooks{})
				finished <- executeErr
			}()
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			joined := false
			defer func() {
				unblock()
				if !joined {
					err = <-finished
				}
			}()
			select {
			case <-entered:
			case err = <-finished:
				joined = true
				t.Fatalf("finalized-result converter did not enter genuine work: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			actual, actualGeneration, actualFrame, actualDepth := op.Session.Sepgsql.PhaseSnapshot()
			if actual != "T" || actualGeneration != generation || actualFrame == 0 || actualDepth != 1 ||
				grants != 1 || descriptions != 1 || deliveries != 0 || stored == nil || stored.Closed {
				t.Fatal("barrier is not inside the genuine finalized-result trusted conversion", actual, actualGeneration, actualFrame, actualDepth)
			}
			authority := core.ScopeAuthority(scope)
			if authority == nil {
				t.Fatal("INSERT lost its actual initialized command owner")
			}
			owner, read := authority.RoutineInitialization, authority.RoutineCatalog
			mark := scope.MarkSepgsqlObservations()
			if !owner.Active() || read.Owner() == nil || mark.Observations == 0 {
				t.Fatal("finalized INSERT omitted genuine initialization or codec observations")
			}
			// Preserve each actual predecessor pointer, not just the observation
			// count: a retry that refreshed old authority must not pass this proof.
			predecessors := func() [][2]uintptr {
				observations := reflect.ValueOf(scope).Elem().FieldByName("sepgsqlObservations")
				refs := make([][2]uintptr, observations.Len())
				for i := range refs {
					observation := observations.Index(i)
					refs[i] = [2]uintptr{
						observation.FieldByName("ExpectedObject").Elem().Pointer(),
						observation.FieldByName("ExpectedLabel").Pointer(),
					}
				}
				return refs
			}
			before := predecessors()
			switch change {
			case "relabel":
				enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION "+converter+" IS 'U'")
			case "descriptor":
				enginetest.Run(t, e, "ALTER FUNCTION "+converter+" RENAME TO sepgsql_finalized_stale_converter")
			}
			unblock()
			err = <-finished
			joined = true
			if change == "unchanged" {
				if err != nil || deliveries != 1 || result.Count != 1 || result.CommandTag != "INSERT 0 1" {
					t.Fatal("healthy finalized INSERT output", result, deliveries, err)
				}
			} else if err != (catalog.Conflict{}) || err.Error() != "stuntgres: catalog publication conflict" || deliveries != 0 {
				t.Fatal("stale finalized INSERT authority did not veto fields with the exact currentness diagnostic", deliveries, err)
			}
			actual, actualGeneration, actualFrame, actualDepth = op.Session.Sepgsql.PhaseSnapshot()
			if actual != subject || actualGeneration != generation || actualFrame != frame || actualDepth != depth {
				t.Fatal("finalized codec did not balance the original subject phase", actual, actualGeneration, actualFrame, actualDepth)
			}
			if grants != 1 || descriptions != 1 || scope.MarkSepgsqlObservations() != mark ||
				core.ScopeAuthority(scope) != authority || authority.RoutineInitialization != owner || authority.RoutineCatalog != read ||
				!reflect.DeepEqual(predecessors(), before) {
				t.Fatal("finalized encoding retried or refreshed the captured authority")
			}
			if result.Rows != nil || !stored.Closed || scope.Owner.CopyLease != nil ||
				!reflect.ValueOf(core.ScopeProtocol(scope)).Elem().FieldByName("simplePortal").IsNil() {
				t.Fatal("finalized output retained a store, portal or I/O reservation")
			}
			if finishErr := scope.Finish(err); !errors.Is(finishErr, err) {
				t.Fatal("Finish changed finalized output outcome", finishErr, err)
			}
			scopeFinished = true
			count := enginetest.Tuples(enginetest.Run(t, e, "SELECT count(*) FROM sepgsql_finalized_codec"))
			want := int64(0)
			if change == "unchanged" {
				want = 1
			}
			if len(count) != 1 || len(count[0]) != 1 || count[0][0] != want {
				t.Fatal("finalized output did not preserve INSERT commit/rollback behavior", count, want)
			}
		})
	}
	t.Run("copy_query_same_owner_across_suspension", func(t *testing.T) {
		e, _ := sepgsqlCodecEngine(t, "S")
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean) IS 'S'")
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "SET client_encoding='LATIN1'")
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(nil)
		core.SetProtocolScopeCommand(core.ScopeProtocol(scope), true)
		var header reflect.Value
		var owner, initializations uint64
		var owners map[uint64]bool
		grants := 0
		original := op.Ctx
		defer func() { op.Ctx = original }()
		op.Ctx = workadmission.With(original, func(_ context.Context, class workadmission.Class) error {
			if class != workadmission.WorkEncodingConvert {
				return nil
			}
			grants++
			if !header.IsValid() || !header.FieldByName("Host").IsNil() ||
				header.FieldByName("RoutineInitialization").FieldByName("instance").Uint() != owner ||
				header.FieldByName("RoutineCatalog").IsZero() {
				return errors.New("COPY pull lost its same suspended source attachment")
			}
			if scope.MarkSepgsqlObservations().Initializations != initializations ||
				!reflect.DeepEqual(sepgsqlInitializationOwners(scope, exec.RetainedExpressionInitialization, ids.ObjectAddress{}), owners) {
				return errors.New("COPY row reminted source initialization")
			}
			if op.Session.Sepgsql.Current() != "T" {
				return errors.New("actual encoding kernel lost its separate trusted borrower")
			}
			return nil
		})
		hooks := core.CopyProtocolHooks{Start: func(meta core.CopyProtocolMetadata) error {
			if meta.From || meta.Format != 0 || len(meta.ColumnFormats) != 1 {
				return errors.New("unexpected actual COPY response")
			}
			authority := reflect.ValueOf(scope).Elem().FieldByName("authority")
			if authority.IsNil() {
				return errors.New("COPY omitted its genuine source header")
			}
			header = authority.Elem()
			owner = header.FieldByName("RoutineInitialization").FieldByName("instance").Uint()
			owners = sepgsqlInitializationOwners(scope, exec.RetainedExpressionInitialization, ids.ObjectAddress{})
			initializations = scope.MarkSepgsqlObservations().Initializations
			if owner == 0 || !owners[owner] || !header.FieldByName("Host").IsNil() || header.FieldByName("RoutineCatalog").IsZero() {
				return errors.New("COPY response did not suspend an actually initialized source")
			}
			return nil
		}}
		var output bytes.Buffer
		query := enginetest.Statement(t, "COPY (SELECT repeat('é',n) FROM (VALUES(1),(2)) encoding_rows(n) ORDER BY n) TO STDOUT").(*parser.Copy)
		count, copyErr := core.ScopeCopyProtocol(scope, query, nil, &output, hooks)
		op.Ctx = original
		if copyErr != nil || count != 2 || output.String() != "\xe9\n\xe9\xe9\n" || grants < 2 {
			t.Fatal("actual multirow COPY source/conversion", count, output.Bytes(), grants, copyErr)
		}
		if !header.FieldByName("Host").IsNil() || !header.FieldByName("RoutineInitialization").IsZero() ||
			!header.FieldByName("RoutineCatalog").IsZero() || core.ForeignTableScopeCursor(core.ScopeForeignTablesOrZero(scope)) != nil {
			t.Fatal("terminal COPY retained a borrowed source attachment")
		}
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("multirow COPY leaked the converter subject")
		}
	})
	for _, label := range []string{"S", "U"} {
		t.Run(label, func(t *testing.T) {
			e, typ := sepgsqlCodecEngine(t, "S")
			for _, helper := range []string{"utf8_to_iso8859_1", "iso8859_1_to_utf8"} {
				enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog."+helper+"(integer,integer,cstring,internal,integer,boolean) IS '"+label+"'")
			}
			enginetest.Run(t, e, "CREATE TABLE sepgsql_copy_codec(v hstore)")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, "SET client_encoding='LATIN1'")
			enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := scope.RoutineAuthorizationLocked()
			if err == nil {
				err = scope.CaptureIdentityLocked(auth)
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			read := auth.Catalog
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: read}}
			output := evaluation.FunctionOutput()
			output.ByteTransport, output.Transport = true, op.EffectiveSettings().ClientEncoding()
			value, err := typ.InputWithEnvironment(op.Context(), "k=>é", -1, evaluation.FunctionInput())
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			fields, _, err := session.EncodeProtocolRow(op.Context(), []sqltype.Value{value}, []int16{0}, output, nil, nil)
			if label == "U" {
				enginetest.State(t, err, "42501")
			} else if err != nil || len(fields) != 1 || string(fields[0].Bytes) != "\"k\"=>\"\xe9\"" {
				t.Fatal("actual protocol output conversion", fields, err)
			}
			input := evaluation.FunctionInput()
			input.ByteTransport, input.Transport = true, output.Transport
			raw := "k=>\xe9"
			data := make([]byte, 4+len(raw))
			binary.BigEndian.PutUint32(data, uint32(len(raw)))
			copy(data[4:], raw)
			frame := make([]sqltype.Value, 1)
			var diagnostic session.ProtocolContext
			err = session.ImportProtocolParameters(op.Context(), output, &enginetest.ProtocolReader{Data: data}, []int16{0}, 1, []sqltype.Usage{typ.Usage()}, frame, input, &diagnostic, session.ProtocolBindInput)
			if label == "U" {
				enginetest.State(t, err, "42501")
				if !frame[0].IsNull() && frame[0].Type() != nil {
					t.Fatal("denied protocol input published a datum")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				text, err := typ.OutputWithEnvironment(op.Context(), frame[0], evaluation.FunctionOutput())
				if err != nil || text != `"k"=>"é"` {
					t.Fatal("actual protocol input bytes", text, err)
				}
			}
			if op.Session.Sepgsql.Current() != "R" {
				t.Fatal("protocol codec leaked trusted subject")
			}
			scope.Finish(err)

			// These are the actual COPY owner/response and stream producers, not
			// a standalone converter approximation or an activated CLI server.
			for _, from := range []bool{false, true} {
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(nil)
				core.SetProtocolScopeCommand(core.ScopeProtocol(scope), true)
				var output bytes.Buffer
				query := "COPY (SELECT 'é'::text) TO STDOUT"
				if from {
					query = "COPY sepgsql_copy_codec FROM STDIN"
				}
				count, copyErr := core.ScopeCopyProtocol(scope, enginetest.Statement(t, query).(*parser.Copy), strings.NewReader("k=>\xe9\n"), &output, core.CopyProtocolHooks{})
				if label == "U" {
					enginetest.State(t, copyErr, "42501")
					if count != 0 || output.Len() != 0 {
						t.Fatal("denied COPY published prefix", count, output.String())
					}
				} else {
					if copyErr != nil || count != 1 {
						t.Fatal("actual COPY conversion", count, copyErr)
					}
					if !from && output.String() != "\xe9\n" {
						t.Fatalf("COPY output bytes %x", output.Bytes())
					}
				}
				if op.Session.Sepgsql.Current() != "R" {
					t.Fatal("COPY conversion leaked subject")
				}
				scope.Finish(copyErr)
			}
		})
	}
}
