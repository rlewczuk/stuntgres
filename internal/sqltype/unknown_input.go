package sqltype

import "fmt"

// PlanUnknownInputTransport executes the output/input pair of an explicit
// string-category cast to UNKNOWN using concrete TEXT for the identical input
// bytes. It deliberately bypasses ordinary casts to TEXT (notably bpchar's
// padding-removing conversion). The logical UNKNOWN target belongs to analysis.
func PlanUnknownInputTransport(source, text Usage) (CoercionPlan, error) {
	if !source.Valid() || !text.Valid() || !source.Type.Concrete() || text.Type.ID() != Text || text != text.Type.Usage() || !castStringCategory(source.Type.BaseType()) {
		return CoercionPlan{}, fmt.Errorf("invalid unknown input transport")
	}
	p := CoercionPlan{source: source.BaseUsage(), target: text, context: Explicit, method: viaIO, unknownInput: true}
	if source.Type.IsDomain() {
		p.logicalSource, p.logicalTarget = source.Type, text.Type
	}
	return p, nil
}

func (p CoercionPlan) IsUnknownInputTransport() bool { return p.unknownInput }
