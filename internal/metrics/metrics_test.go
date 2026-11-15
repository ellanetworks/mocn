package metrics

import "testing"

func TestProcedureLabel(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"InitialContextSetup":                "initial_context_setup",
		"ERABSetup":                          "erab_setup",
		"ERABModificationIndication":         "erab_modification_indication",
		"PDUSessionResourceModifyIndication": "pdu_session_resource_modify_indication",
		"HandoverResourceAllocation":         "handover_resource_allocation",
		"":                                   "",
	} {
		if got := ProcedureLabel(name); got != want {
			t.Errorf("ProcedureLabel(%q) = %q, want %q", name, got, want)
		}
	}
}
