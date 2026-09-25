package dto

import (
	"encoding/json"
	"fermentation-kinetics-deviation-analysis/backend/internal/model"
	"time"
)

type RunDeviationAnalysisRequest struct {
	SensorSeriesID uint `json:"sensor_series_id" binding:"required"`
}
type DeviationAnalysisTransitionRequest struct {
	ToState string `json:"to_state" binding:"required,oneof=reviewed confirmed investigating voided"`
	Comment string `json:"comment" binding:"omitempty,max=1000"`
}
type PhaseReviewRequest struct {
	Phase    string `json:"phase" binding:"required,oneof=lag growth production harvest"`
	Decision string `json:"decision" binding:"required,oneof=accepted follow_up false_alarm"`
	Comment  string `json:"comment" binding:"required,min=1,max=500"`
}
type PhaseReviewResponse struct {
	ID             uint      `json:"id"`
	AnalysisID     uint      `json:"analysis_id"`
	Phase          string    `json:"phase"`
	Decision       string    `json:"decision"`
	Comment        string    `json:"comment"`
	ReviewedBy     uint      `json:"reviewed_by"`
	ReviewedByName string    `json:"reviewed_by_name"`
	SubmittedAt    time.Time `json:"submitted_at"`
}
type DeviationAnalysisQuery struct {
	SensorSeriesID, RecipeID uint
	State, Level, Initiator  string
	Page, PageSize           int
}
type DeviationAnalysisResponse struct {
	ID                   uint                  `json:"id"`
	SensorSeriesID       uint                  `json:"sensor_series_id"`
	RecipeID             uint                  `json:"recipe_id"`
	RecipeVersion        int                   `json:"recipe_version"`
	AlgorithmVersion     string                `json:"algorithm_version"`
	InputHash            string                `json:"input_hash"`
	PhaseScoresJSON      json.RawMessage       `json:"phase_scores_json"`
	DeviationLevel       string                `json:"deviation_level"`
	AlignedCurveJSON     json.RawMessage       `json:"aligned_curve_json"`
	SuspectedCausesJSON  json.RawMessage       `json:"suspected_causes_json"`
	AnalysisState        string                `json:"analysis_state"`
	Explanation          string                `json:"explanation"`
	AnalyzedAt           time.Time             `json:"analyzed_at"`
	InitiatedBy          uint                  `json:"initiated_by"`
	InitiatedByName      string                `json:"initiated_by_name"`
	ReviewedBy           *uint                 `json:"reviewed_by,omitempty"`
	ReviewedByName       string                `json:"reviewed_by_name,omitempty"`
	DurationMilliseconds int64                 `json:"duration_milliseconds"`
	FailureReason        string                `json:"failure_reason,omitempty"`
	ReviewComment        string                `json:"review_comment,omitempty"`
	ReplayVerified       *bool                 `json:"replay_verified,omitempty"`
	PhaseReviews         []PhaseReviewResponse `json:"phase_reviews"`
	SensorSeries         *SensorSeriesResponse `json:"sensor_series,omitempty"`
	CreatedAt            time.Time             `json:"created_at"`
	UpdatedAt            time.Time             `json:"updated_at"`
}
type DeviationAnalysisListResponse struct {
	Items []DeviationAnalysisResponse `json:"items"`
	Total int64                       `json:"total"`
	Page  int                         `json:"page"`
	Size  int                         `json:"page_size"`
}

func NewDeviationAnalysisResponse(analysis model.DeviationAnalysis) DeviationAnalysisResponse {
	response := DeviationAnalysisResponse{
		ID: analysis.ID, SensorSeriesID: analysis.SensorSeriesID, RecipeID: analysis.RecipeID,
		RecipeVersion: analysis.RecipeVersion, AlgorithmVersion: analysis.AlgorithmVersion,
		InputHash: analysis.InputHash, PhaseScoresJSON: rawJSON(analysis.PhaseScoresJSON),
		DeviationLevel: analysis.DeviationLevel, AlignedCurveJSON: rawJSON(analysis.AlignedCurveJSON),
		SuspectedCausesJSON: rawJSON(analysis.SuspectedCausesJSON), AnalysisState: analysis.AnalysisState,
		Explanation: analysis.Explanation, AnalyzedAt: analysis.AnalyzedAt,
		InitiatedBy: analysis.InitiatedBy, InitiatedByName: analysis.InitiatedByName,
		ReviewedBy: analysis.ReviewedBy, ReviewedByName: analysis.ReviewedByName,
		DurationMilliseconds: analysis.DurationMilliseconds, FailureReason: analysis.FailureReason,
		ReviewComment: analysis.ReviewComment, ReplayVerified: analysis.ReplayVerified,
		CreatedAt: analysis.CreatedAt, UpdatedAt: analysis.UpdatedAt,
	}
	if analysis.SensorSeries.ID != 0 {
		s := NewSensorSeriesResponse(analysis.SensorSeries)
		response.SensorSeries = &s
	}
	response.PhaseReviews = make([]PhaseReviewResponse, 0, len(analysis.PhaseReviews))
	for _, review := range analysis.PhaseReviews {
		response.PhaseReviews = append(response.PhaseReviews, PhaseReviewResponse{
			ID: review.ID, AnalysisID: review.AnalysisID, Phase: review.Phase,
			Decision: review.Decision, Comment: review.Comment,
			ReviewedBy: review.ReviewedBy, ReviewedByName: review.ReviewedByName,
			SubmittedAt: review.SubmittedAt,
		})
	}
	return response
}
