package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"fermentation-kinetics-deviation-analysis/backend/internal/algorithm"
	"fermentation-kinetics-deviation-analysis/backend/internal/constants"
	"fermentation-kinetics-deviation-analysis/backend/internal/dto"
	"fermentation-kinetics-deviation-analysis/backend/internal/model"
	"fermentation-kinetics-deviation-analysis/backend/internal/repository"
	"fermentation-kinetics-deviation-analysis/backend/internal/timeseries"
	"fermentation-kinetics-deviation-analysis/backend/internal/util"
)

func TestAnalysisIdempotencyReviewerSeparationAndReplay(t *testing.T) {
	db := newTestDB(t)
	vesselRepo := repository.NewFermentationVesselRepository(db)
	recipeRepo := repository.NewCultureRecipeRepository(db)
	seriesRepo := repository.NewSensorSeriesRepository(db)
	analysisRepo := repository.NewDeviationAnalysisRepository(db)
	auditRepo := repository.NewAuditRepository(db)
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	vessel := model.FermentationVessel{
		VesselCode: "FV-A1", Name: "Analysis vessel", WorkingVolumeL: 100,
		SensorChannels: `["ph"]`, Location: "Lab", OwnerTeam: "Process",
		VesselState: "active", CommissionedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := vesselRepo.Create(context.Background(), &vessel); err != nil {
		t.Fatal(err)
	}
	boundaries, references, tolerances := testRecipeConfig(t)
	recipe := model.CultureRecipe{
		VesselID: vessel.ID, RecipeCode: "ANALYSIS-A", Version: 1, Organism: "Test organism",
		TargetDurationH: 8, PhaseBoundariesJSON: string(boundaries), ReferenceCurvesJSON: string(references),
		ToleranceProfileJSON: string(tolerances), RecipeState: "published",
		CreatedBy: 8, CreatedByName: "scientist", CreatedAt: now, UpdatedAt: now,
	}
	if err := recipeRepo.Create(context.Background(), &recipe); err != nil {
		t.Fatal(err)
	}
	points := make([]timeseries.Point, 0, 9)
	for hour := 0; hour <= 8; hour++ {
		value := 7 - float64(hour)*0.05
		valueCopy := value
		points = append(points, timeseries.Point{
			Timestamp: now.Add(time.Duration(hour) * time.Hour), Values: map[string]*float64{"ph": &valueCopy},
		})
	}
	pointsJSON, err := timeseries.EncodePoints(points)
	if err != nil {
		t.Fatal(err)
	}
	series := model.SensorSeries{
		VesselID: vessel.ID, RecipeID: recipe.ID, RunCode: "RUN-A1", Channel: "ph",
		SampleIntervalS: 3600, PointsJSON: pointsJSON, StartedAt: now, EndedAt: now.Add(8 * time.Hour),
		SourceChecksum: util.HashString(pointsJSON), SeriesState: "ready", QualitySummary: `{"valid":true}`,
		NormalizationJSON: `{"method":"median_iqr"}`, ImportedBy: 9, ImportedByName: "analyst",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := seriesRepo.Create(context.Background(), &series); err != nil {
		t.Fatal(err)
	}
	svc := NewDeviationAnalysisService(analysisRepo, repository.NewAnalysisPhaseReviewRepository(db), recipeRepo, seriesRepo, auditRepo, algorithm.NewEvaluator())
	initiator := util.Actor{UserID: 9, Username: "analyst", Role: "data_analyst", RequestID: "req-run"}
	first, reused, err := svc.Run(context.Background(), dto.RunDeviationAnalysisRequest{SensorSeriesID: series.ID}, "idem-a", initiator)
	if err != nil || reused {
		t.Fatalf("first run reused=%v err=%v", reused, err)
	}
	second, reused, err := svc.Run(context.Background(), dto.RunDeviationAnalysisRequest{SensorSeriesID: series.ID}, "idem-a", initiator)
	if err != nil || !reused || second.ID != first.ID {
		t.Fatalf("same-key run id=%d reused=%v err=%v", second.ID, reused, err)
	}
	third, reused, err := svc.Run(context.Background(), dto.RunDeviationAnalysisRequest{SensorSeriesID: series.ID}, "idem-b", initiator)
	if err != nil || !reused || third.ID != first.ID {
		t.Fatalf("same-input run id=%d reused=%v err=%v", third.ID, reused, err)
	}
	reviewer := util.Actor{UserID: 10, Username: "reviewer", Role: "reviewer", RequestID: "req-review"}
	if _, err := svc.Transition(context.Background(), first.ID, dto.DeviationAnalysisTransitionRequest{
		ToState: "reviewed", Comment: "Evidence reviewed.",
	}, reviewer); err != nil {
		t.Fatalf("review transition: %v", err)
	}
	_, err = svc.Transition(context.Background(), first.ID, dto.DeviationAnalysisTransitionRequest{
		ToState: "confirmed", Comment: "Self confirmation must fail.",
	}, initiator)
	var appErr *util.AppError
	if !errors.As(err, &appErr) || appErr.Code != util.CodeReviewerConflict {
		t.Fatalf("self-confirm error=%v, want reviewer conflict", err)
	}
	if _, err := svc.Transition(context.Background(), first.ID, dto.DeviationAnalysisTransitionRequest{
		ToState: "confirmed", Comment: "Independent confirmation.",
	}, reviewer); err != nil {
		t.Fatalf("independent confirm: %v", err)
	}
	replayed, err := svc.Replay(context.Background(), first.ID, reviewer)
	if err != nil || replayed.ReplayVerified == nil || !*replayed.ReplayVerified {
		t.Fatalf("replay verified=%v err=%v", replayed.ReplayVerified, err)
	}
}

func TestRunRequiresReadySeriesAndIdempotencyKey(t *testing.T) {
	db := newTestDB(t)
	svc := NewDeviationAnalysisService(
		repository.NewDeviationAnalysisRepository(db), repository.NewAnalysisPhaseReviewRepository(db),
		repository.NewCultureRecipeRepository(db), repository.NewSensorSeriesRepository(db),
		repository.NewAuditRepository(db), algorithm.NewEvaluator(),
	)
	_, _, err := svc.Run(context.Background(), dto.RunDeviationAnalysisRequest{SensorSeriesID: 99}, "", util.Actor{})
	var appErr *util.AppError
	if !errors.As(err, &appErr) || appErr.Code != util.CodeIdempotency {
		t.Fatalf("missing key error=%v", err)
	}
}

func TestAnalysisRoleContract(t *testing.T) {
	if constants.HasPermission(constants.RoleDataAnalyst, constants.PermissionAnalysisConfirm) {
		t.Fatal("data analyst should not receive confirm permission")
	}
}

func TestPhaseReviewGateUpsertAndLock(t *testing.T) {
	db := newTestDB(t)
	vesselRepo := repository.NewFermentationVesselRepository(db)
	recipeRepo := repository.NewCultureRecipeRepository(db)
	seriesRepo := repository.NewSensorSeriesRepository(db)
	analysisRepo := repository.NewDeviationAnalysisRepository(db)
	auditRepo := repository.NewAuditRepository(db)
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	vessel := model.FermentationVessel{
		VesselCode: "FV-P1", Name: "Phase review vessel", WorkingVolumeL: 100,
		SensorChannels: `["ph"]`, Location: "Lab", OwnerTeam: "Process",
		VesselState: "active", CommissionedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := vesselRepo.Create(context.Background(), &vessel); err != nil {
		t.Fatal(err)
	}
	boundaries, references, tolerances := testRecipeConfig(t)
	recipe := model.CultureRecipe{
		VesselID: vessel.ID, RecipeCode: "ANALYSIS-P", Version: 1, Organism: "Test organism",
		TargetDurationH: 8, PhaseBoundariesJSON: string(boundaries), ReferenceCurvesJSON: string(references),
		ToleranceProfileJSON: string(tolerances), RecipeState: "published",
		CreatedBy: 8, CreatedByName: "scientist", CreatedAt: now, UpdatedAt: now,
	}
	if err := recipeRepo.Create(context.Background(), &recipe); err != nil {
		t.Fatal(err)
	}
	points := make([]timeseries.Point, 0, 9)
	for hour := 0; hour <= 8; hour++ {
		value := 7.6 - float64(hour)*0.05
		valueCopy := value
		points = append(points, timeseries.Point{
			Timestamp: now.Add(time.Duration(hour) * time.Hour), Values: map[string]*float64{"ph": &valueCopy},
		})
	}
	pointsJSON, err := timeseries.EncodePoints(points)
	if err != nil {
		t.Fatal(err)
	}
	series := model.SensorSeries{
		VesselID: vessel.ID, RecipeID: recipe.ID, RunCode: "RUN-P1", Channel: "ph",
		SampleIntervalS: 3600, PointsJSON: pointsJSON, StartedAt: now, EndedAt: now.Add(8 * time.Hour),
		SourceChecksum: util.HashString(pointsJSON), SeriesState: "ready", QualitySummary: `{"valid":true}`,
		NormalizationJSON: `{"method":"median_iqr"}`, ImportedBy: 9, ImportedByName: "analyst",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := seriesRepo.Create(context.Background(), &series); err != nil {
		t.Fatal(err)
	}
	svc := NewDeviationAnalysisService(
		analysisRepo, repository.NewAnalysisPhaseReviewRepository(db), recipeRepo, seriesRepo, auditRepo, algorithm.NewEvaluator(),
	)
	initiator := util.Actor{UserID: 9, Username: "analyst", Role: "data_analyst", RequestID: "req-run-p"}
	reviewer := util.Actor{UserID: 10, Username: "reviewer", Role: "reviewer", RequestID: "req-review-p"}
	result, _, err := svc.Run(context.Background(), dto.RunDeviationAnalysisRequest{SensorSeriesID: series.ID}, "idem-p", initiator)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(result.PendingReviewPhases) != 4 {
		t.Fatalf("pending phases=%v, want all four abnormal phases", result.PendingReviewPhases)
	}
	if _, err := svc.Transition(context.Background(), result.ID, dto.DeviationAnalysisTransitionRequest{
		ToState: "reviewed", Comment: "Evidence checked.",
	}, reviewer); err != nil {
		t.Fatalf("review transition: %v", err)
	}
	_, err = svc.Transition(context.Background(), result.ID, dto.DeviationAnalysisTransitionRequest{
		ToState: "confirmed", Comment: "Premature confirmation.",
	}, reviewer)
	var appErr *util.AppError
	if !errors.As(err, &appErr) || appErr.Code != util.CodeStateTransition {
		t.Fatalf("confirm without dispositions error=%v, want state transition conflict", err)
	}
	if _, err := svc.SubmitPhaseReview(context.Background(), result.ID, dto.SubmitPhaseReviewRequest{
		Phase: "lag", Disposition: "accept_evidence", Note: "   ",
	}, reviewer); !errors.As(err, &appErr) || appErr.Code != util.CodeValidation {
		t.Fatalf("blank note error=%v, want validation failure", err)
	}
	if _, err := svc.SubmitPhaseReview(context.Background(), result.ID, dto.SubmitPhaseReviewRequest{
		Phase: "lag", Disposition: "looks_fine", Note: "Invalid disposition value.",
	}, reviewer); !errors.As(err, &appErr) || appErr.Code != util.CodeValidation {
		t.Fatalf("bad disposition error=%v, want validation failure", err)
	}
	dispositions := map[string]string{
		"lag": "accept_evidence", "growth": "request_follow_up",
		"production": "dismiss_false_positive", "harvest": "accept_evidence",
	}
	for _, phase := range result.PendingReviewPhases {
		if _, err := svc.SubmitPhaseReview(context.Background(), result.ID, dto.SubmitPhaseReviewRequest{
			Phase: phase, Disposition: dispositions[phase], Note: "Disposition recorded for " + phase + ".",
		}, reviewer); err != nil {
			t.Fatalf("submit %s review: %v", phase, err)
		}
	}
	revised, err := svc.SubmitPhaseReview(context.Background(), result.ID, dto.SubmitPhaseReviewRequest{
		Phase: "growth", Disposition: "accept_evidence", Note: "Follow-up evidence arrived; accepting.",
	}, reviewer)
	if err != nil {
		t.Fatalf("revise growth review: %v", err)
	}
	if len(revised.PhaseReviews) != 4 || len(revised.PendingReviewPhases) != 0 {
		t.Fatalf("reviews=%d pending=%v, want 4 dispositions and no pending phases",
			len(revised.PhaseReviews), revised.PendingReviewPhases)
	}
	for _, item := range revised.PhaseReviews {
		if item.Phase == "growth" && (item.Disposition != "accept_evidence" || item.Note != "Follow-up evidence arrived; accepting.") {
			t.Fatalf("growth review was not revised: %+v", item)
		}
		if item.SubmittedByName != "reviewer" || item.SubmittedAt.IsZero() {
			t.Fatalf("submitter evidence missing: %+v", item)
		}
	}
	confirmed, err := svc.Transition(context.Background(), result.ID, dto.DeviationAnalysisTransitionRequest{
		ToState: "confirmed", Comment: "All phases disposed.",
	}, reviewer)
	if err != nil || confirmed.AnalysisState != "confirmed" {
		t.Fatalf("confirm after dispositions: state=%s err=%v", confirmed.AnalysisState, err)
	}
	_, err = svc.SubmitPhaseReview(context.Background(), result.ID, dto.SubmitPhaseReviewRequest{
		Phase: "lag", Disposition: "dismiss_false_positive", Note: "Late change must be rejected.",
	}, reviewer)
	if !errors.As(err, &appErr) || appErr.Code != util.CodeStateTransition {
		t.Fatalf("post-confirm review error=%v, want locked state conflict", err)
	}
	reopened, err := svc.Get(context.Background(), result.ID)
	if err != nil {
		t.Fatalf("reload confirmed analysis: %v", err)
	}
	if len(reopened.PhaseReviews) != 4 || reopened.PhaseReviews[0].SubmittedByName != "reviewer" {
		t.Fatalf("reopened reviews=%+v, want persisted dispositions with submitter", reopened.PhaseReviews)
	}
	logs, total, err := auditRepo.List(context.Background(), repository.AuditQuery{
		EntityType: "deviation_analysis", Action: "phase_review", Page: 1, PageSize: 50,
	})
	if err != nil || total != 5 {
		t.Fatalf("phase review audit events=%d err=%v, want 5", total, err)
	}
	for _, entry := range logs {
		if entry.ActorName != "reviewer" || entry.CreatedAt.IsZero() || entry.EntityID != result.ID {
			t.Fatalf("audit entry missing actor or timestamp: %+v", entry)
		}
	}
}
