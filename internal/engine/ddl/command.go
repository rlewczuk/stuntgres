package ddl

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// RecordCommand annotates the actual command owner, not descriptor mutations.
// Pure catalog staging hosts need not expose a lexical command collector.
func RecordCommand(stage any, tag string, address ids.ObjectAddress) {
	if collector, ok := stage.(interface {
		RecordDDLCommand(string, ids.ObjectAddress)
	}); ok {
		collector.RecordDDLCommand(tag, address)
	}
}

// CreateCommand and ReplaceCommand mark only an explicitly selected base
// command. Companion descriptors continue using ordinary Stage mutations.
func CreateCommand(stage Stage, tag string, descriptor catalog.Descriptor) error {
	if err := stage.Create(descriptor); err != nil {
		return err
	}
	RecordCommand(stage, tag, descriptor.Address())
	return nil
}

func ReplaceCommand(stage Stage, tag string, descriptor catalog.Descriptor) error {
	if err := stage.Replace(descriptor); err != nil {
		return err
	}
	RecordCommand(stage, tag, descriptor.Address())
	return nil
}

func AdmitNoopCommand(stage any, tag string, address ids.ObjectAddress) {
	if collector, ok := stage.(interface {
		AdmitDDLNoop(string, ids.ObjectAddress)
	}); ok {
		collector.AdmitDDLNoop(tag, address)
	}
}
