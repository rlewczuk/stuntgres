package pgcatalog

import (
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// BuiltinViewName describes one released display reference, not an executable
// object or a descriptor. Missing upstream objects must never enter the catalog.
type BuiltinViewName struct {
	Class, Namespace ids.OID
	Name             string
	Signature        catalog.ProcedureSignature
	RoutineOID       ids.OID
	RegclassLiteral  bool
	IdentifierOnly   bool
}

type builtinViewPart struct {
	text           string
	name           BuiltinViewName
	targetBoundary viewTargetBoundary
}
type builtinView struct {
	oid            ids.OID
	returnRule     ids.OID // Explicit native metadata identity; not an executable rule.
	parts          []builtinViewPart
	prettyParts    []builtinViewPart
	canonical      string
	rules          uint32
	delete         bool
	writable       []int32
	declaration    *builtinRewriteDeclaration
	declarationErr error
}

type viewTargetBoundary uint8

const (
	viewTargetNone viewTargetBoundary = iota
	viewTargetStart
	viewTargetNext
	viewTargetEnd
)

func viewTargetsStart() builtinViewPart {
	return builtinViewPart{text: " SELECT ", targetBoundary: viewTargetStart}
}

func viewTargetSeparator() builtinViewPart {
	return builtinViewPart{text: ",\n    ", targetBoundary: viewTargetNext}
}

func viewTargetsEnd() builtinViewPart {
	return builtinViewPart{targetBoundary: viewTargetEnd}
}

func viewText(text string) builtinViewPart { return builtinViewPart{text: text} }
func viewName(text string, class ids.OID) builtinViewPart {
	namespace, name := ids.PGCatalogNamespaceOID, text
	if strings.HasPrefix(text, "information_schema.") {
		namespace, name = 13229, strings.TrimPrefix(text, "information_schema.")
	}
	return builtinViewPart{text: text, name: BuiltinViewName{Class: class, Namespace: namespace, Name: name}}
}
func viewType(text string) builtinViewPart {
	p := viewName(strings.Trim(text, `"`), ids.TypeClassID)
	p.text = text
	return p
}
func viewIdentifier(name string) builtinViewPart {
	return builtinViewPart{text: name, name: BuiltinViewName{Name: name, IdentifierOnly: true}}
}
func viewClass(name string) builtinViewPart  { return viewName(name, ids.RelationClassID) }
func viewDomain(name string) builtinViewPart { return viewName(name, ids.TypeClassID) }
func viewRoutine(name string, signature catalog.ProcedureSignature) builtinViewPart {
	p := viewName(name, ids.ProcedureClassID)
	p.name.Signature = signature
	if identity, ok := catalog.BuiltinRoutineIdentityBySignature(p.name.Name, signature); ok && identity.Namespace == p.name.Namespace {
		p.name.RoutineOID = identity.ID
	}
	return p
}
func viewRegclass(name string) builtinViewPart {
	p := viewClass(name)
	p.text = "'" + name + "'"
	p.name.RegclassLiteral = true
	return p
}

// Both consumers share the same closed inventory. The canonical strings are
// assembled once; names are formatted only when a definition is disclosed.
var builtinViews = indexBuiltinViews()

func indexBuiltinViews() map[ids.OID]builtinView {
	result := make(map[ids.OID]builtinView, len(pgBuiltinViews)+len(infoBuiltinViews))
	add := func(record builtinView) {
		var text strings.Builder
		for _, part := range record.parts {
			text.WriteString(part.text)
		}
		record.canonical = text.String()
		if record.returnRule != 0 {
			statements, err := parser.ParseRoutineDeclarationBody(record.canonical)
			record.declarationErr = err
			if err == nil {
				record.declaration, record.declarationErr = newBuiltinRewriteDeclaration(record, statements)
			}
		}
		result[record.oid] = record
	}
	for _, record := range pgBuiltinViews {
		add(record)
	}
	for _, record := range infoBuiltinViews {
		add(record)
	}
	return result
}

// SummaryViewDefinition is canonical/default classification and display metadata.
// It grants neither execution nor virtual-source write authority.
func SummaryViewDefinition(oid ids.OID) string { return builtinViews[oid].canonical }

// BuiltinViewDefinition formats the inventory's explicit identity occurrences.
// An optional target formatter lays out explicitly bounded complete targets;
// nil preserves default/pretty bytes without allocating a target collection.
// No rendered SQL is parsed or rewritten. Missing layout metadata returns empty
// text for a requested wrapped form, like missing pretty metadata.
func BuiltinViewDefinition(oid ids.OID, pretty bool, formatTargets func([]string) string, spelling func(BuiltinViewName) string) string {
	record, ok := builtinViews[oid]
	if !ok {
		return ""
	}
	parts := record.parts
	if pretty {
		parts = record.prettyParts
	}
	if len(parts) == 0 {
		return ""
	}
	if formatTargets != nil && parts[0].targetBoundary != viewTargetStart {
		return ""
	}
	var text strings.Builder
	text.Grow(len(record.canonical))
	var target strings.Builder
	var targets []string
	inTargets := false
	for _, part := range parts {
		if formatTargets != nil {
			switch part.targetBoundary {
			case viewTargetStart:
				inTargets = true
				continue
			case viewTargetNext:
				targets = append(targets, target.String())
				target.Reset()
				continue
			case viewTargetEnd:
				targets = append(targets, target.String())
				text.WriteString(formatTargets(targets))
				inTargets = false
				continue
			}
		}
		out := &text
		if inTargets {
			out = &target
		}
		if part.name.Class == 0 && !part.name.IdentifierOnly {
			out.WriteString(part.text)
		} else {
			out.WriteString(spelling(part.name))
		}
	}
	return text.String()
}

// BuiltinViewEvents preserves nil=all versus empty=none. Unconditional rules
// are independent of the requested outputs; automatic I/U needs a direct output.
func BuiltinViewEvents(oid ids.OID, subset []int32) uint32 {
	record := builtinViews[oid]
	events := record.rules
	if record.delete {
		events |= 16
	}
	for _, attribute := range record.writable {
		if subset == nil {
			return events | 12
		}
		for _, selected := range subset {
			if selected == attribute {
				return events | 12
			}
		}
	}
	return events
}
