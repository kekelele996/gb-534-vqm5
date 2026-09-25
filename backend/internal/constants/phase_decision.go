package constants

type PhaseDecision string

const (
	PhaseDecisionAccepted   PhaseDecision = "accepted"
	PhaseDecisionFollowUp   PhaseDecision = "follow_up"
	PhaseDecisionFalseAlarm PhaseDecision = "false_alarm"
)

func (d PhaseDecision) Valid() bool {
	switch d {
	case PhaseDecisionAccepted, PhaseDecisionFollowUp, PhaseDecisionFalseAlarm:
		return true
	default:
		return false
	}
}

func PhaseDecisionValues() []string {
	return []string{
		string(PhaseDecisionAccepted), string(PhaseDecisionFollowUp), string(PhaseDecisionFalseAlarm),
	}
}
