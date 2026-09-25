package constants

type PhaseDisposition string

const (
	DispositionAcceptEvidence  PhaseDisposition = "accept_evidence"
	DispositionRequestFollowUp PhaseDisposition = "request_follow_up"
	DispositionFalsePositive   PhaseDisposition = "dismiss_false_positive"
)

func (d PhaseDisposition) Valid() bool {
	switch d {
	case DispositionAcceptEvidence, DispositionRequestFollowUp, DispositionFalsePositive:
		return true
	default:
		return false
	}
}

func PhaseDispositionValues() []string {
	return []string{
		string(DispositionAcceptEvidence), string(DispositionRequestFollowUp), string(DispositionFalsePositive),
	}
}
