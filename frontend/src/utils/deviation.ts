import type { DeviationLevel } from '../types/enums/deviation-level'

// Thresholds mirror constants.DeviationLevelForScore on the backend.
export function deviationLevelForScore(score: number): DeviationLevel {
  if (score >= 0.65) return 'critical'
  if (score >= 0.4) return 'major'
  if (score >= 0.2) return 'watch'
  return 'normal'
}
