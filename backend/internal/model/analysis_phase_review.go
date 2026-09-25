package model

import "time"

type AnalysisPhaseReview struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	AnalysisID      uint      `gorm:"not null;uniqueIndex:idx_analysis_phase_review" json:"analysis_id"`
	Phase           string    `gorm:"size:24;not null;uniqueIndex:idx_analysis_phase_review" json:"phase"`
	Disposition     string    `gorm:"size:32;not null" json:"disposition"`
	Note            string    `gorm:"type:text;not null" json:"note"`
	SubmittedBy     uint      `gorm:"not null;index" json:"submitted_by"`
	SubmittedByName string    `gorm:"size:80;not null" json:"submitted_by_name"`
	CreatedAt       time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt       time.Time `gorm:"not null" json:"updated_at"`
}

func (AnalysisPhaseReview) TableName() string { return "analysis_phase_reviews" }
