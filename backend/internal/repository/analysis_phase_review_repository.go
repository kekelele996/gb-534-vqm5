package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"fermentation-kinetics-deviation-analysis/backend/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrPhaseReviewClosed = errors.New("analysis no longer accepts phase review dispositions")

type AnalysisPhaseReviewRepository interface {
	ListByAnalysisIDs(context.Context, []uint) ([]model.AnalysisPhaseReview, error)
	UpsertWhileOpen(context.Context, model.AnalysisPhaseReview, []string) error
}

type analysisPhaseReviewRepository struct{ db *gorm.DB }

func NewAnalysisPhaseReviewRepository(db *gorm.DB) AnalysisPhaseReviewRepository {
	return &analysisPhaseReviewRepository{db: db}
}

func (r *analysisPhaseReviewRepository) ListByAnalysisIDs(
	ctx context.Context, analysisIDs []uint,
) ([]model.AnalysisPhaseReview, error) {
	if len(analysisIDs) == 0 {
		return []model.AnalysisPhaseReview{}, nil
	}
	var reviews []model.AnalysisPhaseReview
	if err := r.db.WithContext(ctx).Where("analysis_id IN ?", analysisIDs).
		Order("analysis_id, phase").Find(&reviews).Error; err != nil {
		return nil, fmt.Errorf("list phase reviews: %w", err)
	}
	return reviews, nil
}

// UpsertWhileOpen stores one phase disposition inside a transaction guarded by a
// conditional update on the analysis row, so a confirmed or voided analysis can
// never accept further disposition changes even under concurrent requests.
func (r *analysisPhaseReviewRepository) UpsertWhileOpen(
	ctx context.Context, review model.AnalysisPhaseReview, openStates []string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		guard := tx.Model(&model.DeviationAnalysis{}).
			Where("id = ? AND analysis_state IN ?", review.AnalysisID, openStates).
			Update("updated_at", time.Now().UTC())
		if guard.Error != nil {
			return fmt.Errorf("guard analysis %d for phase review: %w", review.AnalysisID, guard.Error)
		}
		if guard.RowsAffected == 0 {
			return ErrPhaseReviewClosed
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "analysis_id"}, {Name: "phase"}},
			DoUpdates: clause.AssignmentColumns(
				[]string{"disposition", "note", "submitted_by", "submitted_by_name", "updated_at"}),
		}).Create(&review).Error; err != nil {
			return fmt.Errorf("upsert phase review for analysis %d: %w", review.AnalysisID, err)
		}
		return nil
	})
}
