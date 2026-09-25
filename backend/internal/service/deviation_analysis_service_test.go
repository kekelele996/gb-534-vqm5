package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
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
	svc := NewDeviationAnalysisService(
		analysisRepo, repository.NewPhaseReviewRepository(db), recipeRepo, seriesRepo, auditRepo, algorithm.NewEvaluator(),
	)
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
		repository.NewDeviationAnalysisRepository(db), repository.NewPhaseReviewRepository(db),
		repository.NewCultureRecipeRepository(db),
		repository.NewSensorSeriesRepository(db), repository.NewAuditRepository(db), algorithm.NewEvaluator(),
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

func TestPhaseReviewGateFreezeAndAudit(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	vesselRepo := repository.NewFermentationVesselRepository(db)
	recipeRepo := repository.NewCultureRecipeRepository(db)
	seriesRepo := repository.NewSensorSeriesRepository(db)
	analysisRepo := repository.NewDeviationAnalysisRepository(db)
	phaseReviewRepo := repository.NewPhaseReviewRepository(db)
	auditRepo := repository.NewAuditRepository(db)
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	vessel := model.FermentationVessel{
		VesselCode: "FV-B2", Name: "Phase review vessel", WorkingVolumeL: 100,
		SensorChannels: `["ph"]`, Location: "Lab", OwnerTeam: "Process",
		VesselState: "active", CommissionedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := vesselRepo.Create(ctx, &vessel); err != nil {
		t.Fatal(err)
	}
	boundaries, references, tolerances := testRecipeConfig(t)
	recipe := model.CultureRecipe{
		VesselID: vessel.ID, RecipeCode: "ANALYSIS-B", Version: 1, Organism: "Test organism",
		TargetDurationH: 8, PhaseBoundariesJSON: string(boundaries), ReferenceCurvesJSON: string(references),
		ToleranceProfileJSON: string(tolerances), RecipeState: "published",
		CreatedBy: 8, CreatedByName: "scientist", CreatedAt: now, UpdatedAt: now,
	}
	if err := recipeRepo.Create(ctx, &recipe); err != nil {
		t.Fatal(err)
	}
	points := make([]timeseries.Point, 0, 9)
	for hour := 0; hour <= 8; hour++ {
		// Strongly oscillating signal so every phase is abnormal.
		value := 7 + 4*math.Sin(float64(hour))
		points = append(points, timeseries.Point{
			Timestamp: now.Add(time.Duration(hour) * time.Hour), Values: map[string]*float64{"ph": &value},
		})
	}
	pointsJSON, err := timeseries.EncodePoints(points)
	if err != nil {
		t.Fatal(err)
	}
	series := model.SensorSeries{
		VesselID: vessel.ID, RecipeID: recipe.ID, RunCode: "RUN-B2", Channel: "ph",
		SampleIntervalS: 3600, PointsJSON: pointsJSON, StartedAt: now, EndedAt: now.Add(8 * time.Hour),
		SourceChecksum: util.HashString(pointsJSON), SeriesState: "ready", QualitySummary: `{"valid":true}`,
		NormalizationJSON: `{"method":"median_iqr"}`, ImportedBy: 9, ImportedByName: "analyst",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := seriesRepo.Create(ctx, &series); err != nil {
		t.Fatal(err)
	}
	svc := NewDeviationAnalysisService(
		analysisRepo, phaseReviewRepo, recipeRepo, seriesRepo, auditRepo, algorithm.NewEvaluator(),
	)
	initiator := util.Actor{UserID: 9, Username: "analyst", Role: "data_analyst", RequestID: "req-run-b"}
	reviewer := util.Actor{UserID: 10, Username: "reviewer", Role: "reviewer", RequestID: "req-review-b"}
	run, _, err := svc.Run(ctx, dto.RunDeviationAnalysisRequest{SensorSeriesID: series.ID}, "idem-b2", initiator)
	if err != nil {
		t.Fatalf("run analysis: %v", err)
	}
	var abnormal []string
	var phaseScores []algorithm.PhaseEvidence
	if err := json.Unmarshal(run.PhaseScoresJSON, &phaseScores); err != nil {
		t.Fatal(err)
	}
	for _, score := range phaseScores {
		if constants.DeviationLevelForScore(score.WeightedDeviation) != constants.DeviationNormal {
			abnormal = append(abnormal, score.Phase)
		}
	}
	if len(abnormal) == 0 {
		t.Fatal("fixture must produce at least one abnormal phase")
	}
	if _, err := svc.Transition(ctx, run.ID, dto.DeviationAnalysisTransitionRequest{ToState: "reviewed"}, reviewer); err != nil {
		t.Fatalf("review transition: %v", err)
	}
	// Confirmation must stop while abnormal phases lack conclusions.
	_, err = svc.Transition(ctx, run.ID, dto.DeviationAnalysisTransitionRequest{ToState: "confirmed"}, reviewer)
	var appErr *util.AppError
	if !errors.As(err, &appErr) || appErr.Code != util.CodePhaseReviewRequired {
		t.Fatalf("confirm without phase reviews error=%v, want PHASE_REVIEW_REQUIRED", err)
	}
	// The initiator cannot record conclusions for their own analysis.
	_, err = svc.SubmitPhaseReview(ctx, run.ID, dto.PhaseReviewRequest{
		Phase: abnormal[0], Decision: "accepted", Comment: "Looks fine to me.",
	}, initiator)
	if !errors.As(err, &appErr) || appErr.Code != util.CodeReviewerConflict {
		t.Fatalf("initiator phase review error=%v, want reviewer conflict", err)
	}
	// Normal phases cannot be reviewed.
	normalPhase := "lag"
	for _, phase := range constants.FermentationPhaseValues() {
		if !slices.Contains(abnormal, phase) {
			normalPhase = phase
			break
		}
	}
	if !slices.Contains(abnormal, normalPhase) {
		_, err = svc.SubmitPhaseReview(ctx, run.ID, dto.PhaseReviewRequest{
			Phase: normalPhase, Decision: "accepted", Comment: "Not abnormal.",
		}, reviewer)
		if !errors.As(err, &appErr) || appErr.Code != util.CodeValidation {
			t.Fatalf("review normal phase error=%v, want validation error", err)
		}
	}
	for i, phase := range abnormal {
		decision := "accepted"
		comment := "Evidence supports the algorithmic finding."
		if i%2 == 1 {
			decision = "false_alarm"
			comment = "Sensor calibration explains this phase."
		}
		result, submitErr := svc.SubmitPhaseReview(ctx, run.ID, dto.PhaseReviewRequest{
			Phase: phase, Decision: decision, Comment: comment,
		}, reviewer)
		if submitErr != nil {
			t.Fatalf("submit phase review %s: %v", phase, submitErr)
		}
		saved := slices.IndexFunc(result.PhaseReviews, func(r dto.PhaseReviewResponse) bool { return r.Phase == phase })
		if saved < 0 {
			t.Fatalf("phase review for %s missing from response", phase)
		}
		got := result.PhaseReviews[saved]
		if got.ReviewedByName != "reviewer" || got.SubmittedAt.IsZero() || got.Comment != comment {
			t.Fatalf("phase review payload mismatch: %+v", got)
		}
	}
	// Reopening the analysis returns the same conclusions.
	reopened, err := svc.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("reopen analysis: %v", err)
	}
	if len(reopened.PhaseReviews) != len(abnormal) {
		t.Fatalf("reopened analysis has %d phase reviews, want %d", len(reopened.PhaseReviews), len(abnormal))
	}
	// Every conclusion is visible in the audit center with actor and time.
	logs, total, err := auditRepo.List(ctx, repository.AuditQuery{
		EntityType: "deviation_analysis", EntityID: run.ID, Action: "phase_review", Page: 1, PageSize: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != int64(len(abnormal)) {
		t.Fatalf("phase_review audit events=%d, want %d", total, len(abnormal))
	}
	for _, entry := range logs {
		if entry.ActorName != "reviewer" || entry.CreatedAt.IsZero() {
			t.Fatalf("audit event missing actor or timestamp: %+v", entry)
		}
	}
	if confirmed, confirmErr := svc.Transition(ctx, run.ID, dto.DeviationAnalysisTransitionRequest{
		ToState: "confirmed", Comment: "All abnormal phases adjudicated.",
	}, reviewer); confirmErr != nil {
		t.Fatalf("confirm after phase reviews: %v", confirmErr)
	} else if confirmed.AnalysisState != "confirmed" {
		t.Fatalf("state=%s, want confirmed", confirmed.AnalysisState)
	}
	// Conclusions are frozen once the result is confirmed.
	_, err = svc.SubmitPhaseReview(ctx, run.ID, dto.PhaseReviewRequest{
		Phase: abnormal[0], Decision: "follow_up", Comment: "Attempted change after confirmation.",
	}, reviewer)
	if !errors.As(err, &appErr) || appErr.Code != util.CodeStateTransition {
		t.Fatalf("post-confirm phase review error=%v, want invalid state transition", err)
	}
}
