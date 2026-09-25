package dto

import (
	"time"

	"fermentation-kinetics-deviation-analysis/backend/internal/model"
)

type SubmitPhaseReviewRequest struct {
	Phase       string `json:"phase" binding:"required,oneof=lag growth production harvest"`
	Disposition string `json:"disposition" binding:"required,oneof=accept_evidence request_follow_up dismiss_false_positive"`
	Note        string `json:"note" binding:"required,max=500"`
}

type PhaseReviewResponse struct {
	Phase           string    `json:"phase"`
	Disposition     string    `json:"disposition"`
	Note            string    `json:"note"`
	SubmittedBy     uint      `json:"submitted_by"`
	SubmittedByName string    `json:"submitted_by_name"`
	SubmittedAt     time.Time `json:"submitted_at"`
}

func NewPhaseReviewResponse(review model.AnalysisPhaseReview) PhaseReviewResponse {
	return PhaseReviewResponse{
		Phase: review.Phase, Disposition: review.Disposition, Note: review.Note,
		SubmittedBy: review.SubmittedBy, SubmittedByName: review.SubmittedByName,
		SubmittedAt: review.UpdatedAt,
	}
}
