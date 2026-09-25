export const phaseDecisions = ['accepted', 'follow_up', 'false_alarm'] as const
export type PhaseDecision = typeof phaseDecisions[number]

export const phaseDecisionLabels: Record<PhaseDecision, string> = {
  accepted: '认可证据',
  follow_up: '要求补查',
  false_alarm: '排除误报',
}
