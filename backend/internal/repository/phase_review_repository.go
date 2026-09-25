package repository

import (
	"context"
	"fmt"

	"fermentation-kinetics-deviation-analysis/backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PhaseReviewRepository interface {
	ListByAnalysis(ctx context.Context, analysisID uint) ([]model.PhaseReview, error)
	Upsert(ctx context.Context, review model.PhaseReview) error
}

type phaseReviewRepository struct{ db *gorm.DB }

func NewPhaseReviewRepository(db *gorm.DB) PhaseReviewRepository {
	return &phaseReviewRepository{db: db}
}

func (r *phaseReviewRepository) ListByAnalysis(ctx context.Context, analysisID uint) ([]model.PhaseReview, error) {
	var reviews []model.PhaseReview
	if err := r.db.WithContext(ctx).
		Where("analysis_id = ?", analysisID).
		Order("submitted_at DESC, id DESC").
		Find(&reviews).Error; err != nil {
		return nil, fmt.Errorf("list phase reviews for analysis %d: %w", analysisID, err)
	}
	return reviews, nil
}

// Upsert stores the reviewer's judgment for one phase. A unique index on
// (analysis_id, phase) guarantees one conclusion per phase.
func (r *phaseReviewRepository) Upsert(ctx context.Context, review model.PhaseReview) error {
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "analysis_id"}, {Name: "phase"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"decision", "comment", "reviewed_by", "reviewed_by_name",
			"submitted_at", "updated_at",
		}),
	}).Create(&review).Error; err != nil {
		return fmt.Errorf("upsert phase review for analysis %d phase %s: %w", review.AnalysisID, review.Phase, err)
	}
	return nil
}
