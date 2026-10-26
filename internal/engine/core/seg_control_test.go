package core_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typecmds"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func segStoredCells(t *testing.T, e *core.Engine) []sqltype.Value {
	t.Helper()
	view := e.CatalogRead()
	ref := enginetest.CatalogRef(t, e, view, "seg_exact")
	relation := view.MustRelation(ref.Address)
	var values []sqltype.Value
	for _, version := range e.Storage.Relations[ref.Address].Versions {
		v := version.Values[0]
		if v.Type() != relation.Columns[0].Usage.Type {
			t.Fatal("foreign stored descriptor")
		}
		values = append(values, v)
	}
	return values
}

func TestSegControlPayload(t *testing.T) {
	ctx := context.Background()
	source := engine.New()
	// SQL is fixture construction; the assertions inspect actual stored words,
	// destination ownership and snapshot immutability rather than public rows.
	original := "CREATE EXTENSION seg; CREATE TABLE seg_exact(v seg); INSERT INTO seg_exact VALUES ('12.34567890123456'),('1.0 .. 1.00'),('<1.5 .. ~2.50'),('0...'),('...0'),('1.0(+-)0.005'),('-0.00'),(NULL),('0(+-)-1'),('3.4e38(+-)3.4e38');"
	if err := engine.Restore(ctx, source, strings.NewReader(original)); err != nil {
		t.Fatal(err)
	}
	before := segStoredCells(t, source)
	snapshot := source.CatalogRead()
	var dump bytes.Buffer
	if err := engine.Dump(ctx, source, &dump); err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	target.Mu.Lock()
	_, err := target.Catalog.AllocateOIDLocked()
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Restore(ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	after := segStoredCells(t, target)
	if len(after) != len(before) {
		t.Fatal("stored count")
	}
	for i, v := range before {
		if after[i].Type() == v.Type() || after[i].IsNull() != v.IsNull() {
			t.Fatal("destination ownership", i)
		}
		if v.IsNull() {
			continue
		}
		old, err := v.SegDatum()
		if err != nil {
			t.Fatal(err)
		}
		got, err := after[i].SegDatum()
		if err != nil || got != old {
			t.Fatalf("cell %d words/digits/markers %#v want %#v (%v)", i, got, old, err)
		}
		reminted, err := after[i].RemapSeg(v.Type())
		if err != nil || !sqltype.SameDatum(v, reminted) {
			t.Fatal("reminted representation", i, err)
		}
	}
	if snapshot.State() != source.CatalogRead().State() {
		t.Fatal("capture changed source catalog")
	}
	var again bytes.Buffer
	if err = engine.Dump(ctx, target, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("exact deterministic redump", err)
	}
	if err = engine.Reset(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err = engine.Restore(ctx, source, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	restored := segStoredCells(t, source)
	for i, v := range before {
		if v.IsNull() {
			continue
		}
		a, _ := v.SegDatum()
		b, _ := restored[i].SegDatum()
		if a != b || restored[i].Type() == v.Type() {
			t.Fatal("reset exactness", i)
		}
	}
	for i, v := range before {
		if !v.IsNull() {
			a, _ := v.SegDatum()
			b, _ := after[i].SegDatum()
			if a != b {
				t.Fatal("retained immutable datum changed")
			}
		}
	}
}

func TestSegReplayAdmission(t *testing.T) {
	ctx := t.Context()
	source := engine.New()
	if err := engine.Restore(ctx, source, strings.NewReader("CREATE EXTENSION seg;CREATE TABLE seg_exact(v seg);INSERT INTO seg_exact VALUES('1.0 .. 1.00');")); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := engine.Dump(ctx, source, &dump); err != nil {
		t.Fatal(err)
	}
	text := dump.String()
	prefix := "-- stuntgres-domain-null-v1 "
	start := strings.Index(text, prefix)
	if start < 0 {
		t.Fatal("missing exact payload")
	}
	end := strings.IndexByte(text[start:], '\n') + start
	encoded := strings.TrimPrefix(text[start:end], prefix)
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var record typedef.DomainReplayRecord
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	if err = engine.Restore(ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	before := target.CatalogRead().State()
	original := segStoredCells(t, target)[0]
	for _, change := range []func(*typedef.DomainReplayRecord){
		func(r *typedef.DomainReplayRecord) { r.Table = "other" },
		func(r *typedef.DomainReplayRecord) { r.Cells = append(r.Cells, r.Cells[0]) },
		func(r *typedef.DomainReplayRecord) { r.Cells[0].Column = "missing" },
		func(r *typedef.DomainReplayRecord) { r.Cells[0].Value.Seg.LowerDigits = 7 },
		func(r *typedef.DomainReplayRecord) { r.Cells[0].Value.Seg.LowerMarker = '-' },
		func(r *typedef.DomainReplayRecord) { r.Cells[0].Value.Seg.UpperBits = 0x40000000 },
		func(r *typedef.DomainReplayRecord) { r.Cells[0].Value.Kind = "null" },
	} {
		var bad typedef.DomainReplayRecord
		if err = json.Unmarshal(data, &bad); err != nil {
			t.Fatal(err)
		}
		change(&bad)
		line, err := typedef.EncodeDomainReplay(bad)
		if err != nil {
			t.Fatal(err)
		}
		script := text[:start] + line + text[end+1:]
		if err = engine.Restore(ctx, target, strings.NewReader(script)); err == nil {
			t.Fatal("malformed replay accepted")
		}
		if target.CatalogRead().State() != before || !sqltype.SameDatum(original, segStoredCells(t, target)[0]) {
			t.Fatal("rejected replay changed committed state")
		}
	}
	for _, replacement := range []string{"NULL", "CAST('1.0' AS seg)", "'1.00'", "'1.0'||''"} {
		script := strings.Replace(text, "VALUES ('1.0')", "VALUES ("+replacement+")", 1)
		if script == text {
			t.Fatal("missing emitted Seg literal")
		}
		if err = engine.Restore(ctx, target, strings.NewReader(script)); err == nil {
			t.Fatal("non-emitted literal shape accepted", replacement)
		}
		if target.CatalogRead().State() != before || !sqltype.SameDatum(original, segStoredCells(t, target)[0]) {
			t.Fatal("shape rejection changed committed state")
		}
	}
	for _, tc := range []struct {
		name, input, canonical string
		invalid                []func(*sqltype.SegDatum)
	}{
		{"infinity", "3.4e38(+-)3.4e38", "0.0 .. Infinity", []func(*sqltype.SegDatum){
			func(d *sqltype.SegDatum) { d.UpperMarker = '~' },
			func(d *sqltype.SegDatum) { d.LowerBits = 0xff800000 },
			func(d *sqltype.SegDatum) { d.LowerBits = 0x7fc00000 },
			func(d *sqltype.SegDatum) { d.LowerBits, d.LowerMarker, d.LowerDigits = 0xff800000, '-', 0 },
		}},
		{"inverted", "0(+-)-1", "1 .. -1", []func(*sqltype.SegDatum){
			func(d *sqltype.SegDatum) { d.LowerMarker = '<' },
			func(d *sqltype.SegDatum) { d.UpperMarker = '~' },
			func(d *sqltype.SegDatum) { d.UpperDigits = 0 },
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exceptional := engine.New()
			if err := engine.Restore(ctx, exceptional, strings.NewReader("CREATE EXTENSION seg;CREATE TABLE seg_exact(v seg);INSERT INTO seg_exact VALUES('"+tc.input+"');")); err != nil {
				t.Fatal(err)
			}
			payload, err := segStoredCells(t, exceptional)[0].SegDatum()
			if err != nil || payload.Reinputtable() {
				t.Fatal("expected exceptional payload", err)
			}
			replay := func(d sqltype.SegDatum, literal, destination string) string {
				t.Helper()
				r := typedef.DomainReplayRecord{Schema: "public", Table: "seg_exact", Cells: []typedef.DomainReplayCell{{Column: "v", Value: &typedef.DomainReplayNode{Kind: "seg", Seg: &d}}}}
				line, err := typedef.EncodeDomainReplay(r)
				if err != nil {
					t.Fatal(err)
				}
				// PairDomainReplay requires the same schema-qualified target as Dump.
				return "CREATE EXTENSION seg;CREATE TABLE seg_exact(v " + destination + ");\n" + line + "INSERT INTO public.seg_exact VALUES('" + literal + "');"
			}
			reject := func(script, reason string) {
				t.Helper()
				if err := engine.Restore(ctx, target, strings.NewReader(script)); err == nil || !strings.Contains(err.Error(), reason) {
					t.Fatal("expected staged admission rejection", reason, err)
				}
				if target.CatalogRead().State() != before || !sqltype.SameDatum(original, segStoredCells(t, target)[0]) {
					t.Fatal("rejection changed committed state")
				}
				retained := before.MustRelation(enginetest.CatalogRef(t, target, target.CatalogRead(), "seg_exact").Address)
				if retained.Columns[0].Usage.Type != original.Type() {
					t.Fatal("rejection changed retained snapshot descriptor")
				}
				var unchanged bytes.Buffer
				if err := engine.Dump(ctx, target, &unchanged); err != nil || unchanged.String() != text {
					t.Fatal("rejection changed committed dump", err)
				}
			}
			admitted := engine.New()
			if err := engine.Restore(ctx, admitted, strings.NewReader(replay(payload, tc.canonical, "seg"))); err != nil {
				t.Fatal("genuine staged exceptional admission", err)
			}
			if got, err := segStoredCells(t, admitted)[0].SegDatum(); err != nil || got != payload {
				t.Fatal("staged exceptional admission changed payload", got, err)
			}
			reject(replay(payload, "2", "seg"), "mismatched canonical seg replay literal")
			reject(replay(payload, tc.canonical, "text"), "invalid native domain replay value")
			for _, expression := range []string{"NULL", "2", "CAST('" + tc.canonical + "' AS seg)", "'" + tc.canonical + "'||''"} {
				script := replay(payload, tc.canonical, "seg")
				altered := strings.Replace(script, "VALUES('"+tc.canonical+"')", "VALUES("+expression+")", 1)
				if altered == script {
					t.Fatal("missing exceptional replay literal")
				}
				reject(altered, "native replay requires its exact emitted literal")
			}
			for _, corrupt := range tc.invalid {
				bad := payload
				corrupt(&bad)
				reject(replay(bad, tc.canonical, "seg"), "invalid native domain replay value")
			}

			// Compose admission with the real replay environment and genuine
			// destination catalog. Corrupt only cloned authority, never the
			// committed catalog or the retained snapshot.
			op, done := enginetest.RoleOperation(t, exceptional, "postgres")
			defer done()
			scope, err := exceptional.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			}()
			stmt := enginetest.Statement(t, "INSERT INTO public.seg_exact VALUES('"+tc.canonical+"')").(*parser.Insert)
			if err := scope.Gate(stmt); err != nil {
				t.Fatal(err)
			}
			auth, err := core.OperationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			view := auth.Catalog
			ref := enginetest.CatalogRef(t, exceptional, view, "seg_exact")
			relation := view.MustRelation(ref.Address)
			c := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: view, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
			c.Input.ExactSnapshot = true
			retainedValue := segStoredCells(t, exceptional)[0]
			c.Input.DomainChecks = &c
			env := typecmds.DomainReplayEnv(exceptional, &c, op, auth)
			cells := []typedef.DomainReplayCell{{Column: "v", Value: &typedef.DomainReplayNode{Kind: "seg", Seg: &payload}}}
			if admitted, err := typedef.DomainReplayCells(env, stmt, cells, relation, []int{0}); err != nil || len(admitted) != 1 {
				t.Fatal("genuine exceptional admission", err)
			}
			base := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.Columns[0].Usage.Type.ID())})
			installation := view.MustExtension(base.Extension)
			for _, corrupt := range []func(*catalog.State){
				func(s *catalog.State) {
					ext := *installation
					ext.Members = append([]ids.ObjectAddress(nil), installation.Members[:2]...)
					s.SetDescriptor(ext.Address(), &ext)
				},
				func(s *catalog.State) {
					procedure := *view.MustProcedure(installation.Members[1])
					procedure.Implementation = extension.StockIntarray
					s.SetDescriptor(procedure.Address(), &procedure)
				},
			} {
				candidate := catalog.CloneState(view.State())
				corrupt(candidate)
				env.Catalog = exceptional.Catalog.Read(candidate)
				if _, err := typedef.DomainReplayCells(env, stmt, cells, relation, []int{0}); err == nil || err.Error() != "stuntgres: invalid native domain replay value" {
					t.Fatal("expected destination provider rejection", err)
				}
				if exceptional.CatalogRead().State() != view.State() || view.MustType(base.Address()) != base || !sqltype.SameDatum(retainedValue, segStoredCells(t, exceptional)[0]) {
					t.Fatal("authority rejection changed committed snapshot")
				}
			}
		})
	}

	// This valid ordinary shape renders beyond float4's input range. Merely
	// matching canonical output would admit it; independent binding must fail.
	ordinary := sqltype.SegDatum{LowerBits: 0x7f7fffff, UpperBits: 0x7f7fffff, LowerDigits: 4, UpperDigits: 4}
	if !ordinary.Reinputtable() {
		t.Fatal("ordinary fixture unexpectedly exceptional")
	}
	ordinaryValue, err := sqltype.SegValue(original.Type(), ordinary)
	if err != nil {
		t.Fatal("invalid ordinary fixture", err)
	}
	if canonical, err := original.Type().Output(ctx, ordinaryValue); err != nil || canonical != "3.403e+38" {
		t.Fatal("ordinary fixture must match the emitted canonical literal", canonical, err)
	}
	r := typedef.DomainReplayRecord{Schema: "public", Table: "seg_exact", Cells: []typedef.DomainReplayCell{{Column: "v", Value: &typedef.DomainReplayNode{Kind: "seg", Seg: &ordinary}}}}
	line, err := typedef.EncodeDomainReplay(r)
	if err != nil {
		t.Fatal(err)
	}
	script := "CREATE EXTENSION seg;CREATE TABLE seg_exact(v seg);\n" + line + "INSERT INTO public.seg_exact VALUES('3.403e+38');"
	var diagnostic *sqlstate.Error
	if err := engine.Restore(ctx, target, strings.NewReader(script)); !errors.As(err, &diagnostic) || diagnostic.Code != "22003" || diagnostic.Message != `"3.403e+38" is out of range for type real` {
		t.Fatal("ordinary replay did not require independent input binding", err)
	}
	if target.CatalogRead().State() != before || !sqltype.SameDatum(original, segStoredCells(t, target)[0]) {
		t.Fatal("ordinary binding rejection changed committed state")
	}
	var unchanged bytes.Buffer
	if err := engine.Dump(ctx, target, &unchanged); err != nil || unchanged.String() != text {
		t.Fatal("ordinary binding rejection changed committed dump", err)
	}
}
