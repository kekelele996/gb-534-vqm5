export const phaseDispositions = ['accept_evidence', 'request_follow_up', 'dismiss_false_positive'] as const
export type PhaseDisposition = typeof phaseDispositions[number]
export const phaseDispositionLabels: Record<PhaseDisposition, string> = {
  accept_evidence: '认可证据',
  request_follow_up: '要求补查',
  dismiss_false_positive: '排除误报',
}
