import { api, json, query } from './client'
import type { Page, ListQuery } from '../types/common'
import type { AnalysisState, DeviationAnalysis } from '../types/deviation-analysis'
import type { PhaseDisposition } from '../types/enums/phase-disposition'

export const listAnalyses = (params: ListQuery & { state?: string; deviation_level?: string } = {}) =>
  api<Page<DeviationAnalysis>>(`/deviation-analyses${query(params)}`)
export const runAnalysis = (sensorSeriesId: number, idempotencyKey: string) =>
  api<DeviationAnalysis>('/deviation-analyses', json('POST', { sensor_series_id: sensorSeriesId }, { 'Idempotency-Key': idempotencyKey }))
export const transitionAnalysis = (id: number, toState: AnalysisState, comment = '') =>
  api<DeviationAnalysis>(`/deviation-analyses/${id}/transition`, json('POST', { to_state: toState, comment }))
export const submitPhaseReview = (id: number, phase: string, disposition: PhaseDisposition, note: string) =>
  api<DeviationAnalysis>(`/deviation-analyses/${id}/phase-reviews`, json('POST', { phase, disposition, note }))
export const replayAnalysis = (id: number) =>
  api<DeviationAnalysis>(`/deviation-analyses/${id}/replay`, json('POST'))
