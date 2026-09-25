package service
import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"fermentation-kinetics-deviation-analysis/backend/internal/algorithm"
	"fermentation-kinetics-deviation-analysis/backend/internal/constants"
	"fermentation-kinetics-deviation-analysis/backend/internal/dto"
	"fermentation-kinetics-deviation-analysis/backend/internal/model"
	"fermentation-kinetics-deviation-analysis/backend/internal/repository"
	"fermentation-kinetics-deviation-analysis/backend/internal/util"
	"gorm.io/gorm"
)
type DeviationAnalysisService struct {
	analyses     repository.DeviationAnalysisRepository
	phaseReviews repository.AnalysisPhaseReviewRepository
	recipes      repository.CultureRecipeRepository
	series       repository.SensorSeriesRepository
	audits       repository.AuditRepository
	evaluator    *algorithm.Evaluator
	now          func() time.Time
}

// phaseReviewOpenStates are the analysis states in which a reviewer may still
// record or revise per-phase dispositions; confirmed and voided results are closed.
var phaseReviewOpenStates = []string{
	string(constants.AnalysisCompleted), string(constants.AnalysisReviewed), string(constants.AnalysisInvestigating),
}

func NewDeviationAnalysisService(
	analyses repository.DeviationAnalysisRepository,
	phaseReviews repository.AnalysisPhaseReviewRepository,
	recipes repository.CultureRecipeRepository,
	series repository.SensorSeriesRepository,
	audits repository.AuditRepository,
	evaluator *algorithm.Evaluator,
) *DeviationAnalysisService {
	return &DeviationAnalysisService{
		analyses: analyses, phaseReviews: phaseReviews, recipes: recipes, series: series, audits: audits,
		evaluator: evaluator,
		now:       func() time.Time { return time.Now().UTC() },
	}
}
func (s *DeviationAnalysisService) Run(
	ctx context.Context, request dto.RunDeviationAnalysisRequest, idempotencyKey string, actor util.Actor,
) (dto.DeviationAnalysisResponse, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return dto.DeviationAnalysisResponse{}, false, util.NewError(http.StatusBadRequest, util.CodeIdempotency, "Idempotency-Key header is required")
	}
	if len(idempotencyKey) > 128 {
		return dto.DeviationAnalysisResponse{}, false, util.NewError(http.StatusBadRequest, util.CodeValidation, "Idempotency-Key must not exceed 128 characters")
	}
	series, err := s.series.GetByID(ctx, request.SensorSeriesID, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dto.DeviationAnalysisResponse{}, false, util.NotFound("sensor series")
		}
		return dto.DeviationAnalysisResponse{}, false, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to load sensor series", err)
	}
	if !series.Ready() {
		return dto.DeviationAnalysisResponse{}, false, util.NewError(http.StatusConflict, util.CodeStateTransition, "only ready sensor series may be analyzed")
	}
	recipe, err := s.recipes.GetByID(ctx, series.RecipeID, false)
	if err != nil {
		return dto.DeviationAnalysisResponse{}, false, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to load frozen recipe version", err)
	}
	snapshot := algorithm.NewSnapshot(series, recipe)
	inputHash, err := snapshot.Hash()
	if err != nil {
		return dto.DeviationAnalysisResponse{}, false, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to hash analysis input", err)
	}
	if prior, findErr := s.analyses.FindByIdempotencyKey(ctx, idempotencyKey); findErr == nil {
		if prior.InputHash != inputHash {
			return dto.DeviationAnalysisResponse{}, false, util.NewError(http.StatusConflict, util.CodeConflict, "Idempotency-Key is already bound to a different input")
		}
		response, responseErr := s.withReviews(ctx, prior)
		return response, true, responseErr
	} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return dto.DeviationAnalysisResponse{}, false, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to check idempotency key", findErr)
	}
	if prior, findErr := s.analyses.FindByInput(ctx, inputHash, algorithm.Version); findErr == nil {
		response, responseErr := s.withReviews(ctx, prior)
		return response, true, responseErr
	} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return dto.DeviationAnalysisResponse{}, false, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to check frozen input", findErr)
	}
	snapshotJSON, err := snapshot.Canonical()
	if err != nil {
		return dto.DeviationAnalysisResponse{}, false, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to freeze analysis input", err)
	}
	now := s.now()
	analysis := model.DeviationAnalysis{
		SensorSeriesID: series.ID, RecipeID: recipe.ID, RecipeVersion: recipe.Version,
		AlgorithmVersion: algorithm.Version, InputHash: inputHash, InputSnapshot: snapshotJSON,
		PhaseScoresJSON: "[]", DeviationLevel: string(constants.DeviationNormal),
		AlignedCurveJSON: "[]", SuspectedCausesJSON: "[]", AnalysisState: string(constants.AnalysisQueued),
		Explanation: "Analysis is queued.", AnalyzedAt: now, InitiatedBy: actor.UserID,
		InitiatedByName: actor.Username, IdempotencyKey: idempotencyKey, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.analyses.Create(ctx, &analysis); err != nil {
		if prior, findErr := s.analyses.FindByInput(ctx, inputHash, algorithm.Version); findErr == nil {
			response, responseErr := s.withReviews(ctx, prior)
			return response, true, responseErr
		}
		return dto.DeviationAnalysisResponse{}, false, util.WrapError(http.StatusConflict, util.CodeConflict, "analysis was queued concurrently", err)
	}
	changed, err := s.analyses.Transition(ctx, analysis.ID, string(constants.AnalysisQueued), string(constants.AnalysisAnalyzing), nil)
	if err != nil || !changed {
		return dto.DeviationAnalysisResponse{}, false, util.WrapError(http.StatusConflict, util.CodeConflict, "analysis could not enter analyzing state", err)
	}
	started := time.Now()
	result, evaluateErr := s.evaluator.Evaluate(snapshot)
	duration := time.Since(started).Milliseconds()
	if evaluateErr != nil {
		_, transitionErr := s.analyses.Transition(ctx, analysis.ID, string(constants.AnalysisAnalyzing), string(constants.AnalysisFailed),
			map[string]any{"failure_reason": util.CompactText(evaluateErr.Error(), 1000), "duration_milliseconds": duration})
		if transitionErr != nil {
			return dto.DeviationAnalysisResponse{}, false, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "analysis failed and failure state could not be stored", transitionErr)
		}
		return dto.DeviationAnalysisResponse{}, false, util.WrapError(http.StatusUnprocessableEntity, util.CodeValidation, "deviation analysis failed", evaluateErr)
	}
	changed, err = s.analyses.Complete(ctx, analysis.ID, map[string]any{
		"phase_scores_json": result.PhaseScoresJSON, "deviation_level": string(result.DeviationLevel),
		"aligned_curve_json": result.AlignedCurveJSON, "suspected_causes_json": result.SuspectedCausesJSON,
		"explanation": result.Explanation, "analyzed_at": s.now(), "duration_milliseconds": duration,
	})
	if err != nil {
		return dto.DeviationAnalysisResponse{}, false, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to store analysis result", err)
	}
	if !changed {
		return dto.DeviationAnalysisResponse{}, false, util.NewError(http.StatusConflict, util.CodeConflict, "analysis state changed concurrently")
	}
	analysis, err = s.analyses.GetByID(ctx, analysis.ID, true)
	if err != nil {
		return dto.DeviationAnalysisResponse{}, false, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to reload deviation analysis", err)
	}
	if err := recordAudit(ctx, s.audits, actor, "deviation_analysis", analysis.ID, "run", nil, analysis,
		inputHash, algorithm.Version, duration); err != nil {
		return dto.DeviationAnalysisResponse{}, false, err
	}
	response, err := s.withReviews(ctx, analysis)
	return response, false, err
}
func (s *DeviationAnalysisService) withReviews(
	ctx context.Context, analysis model.DeviationAnalysis,
) (dto.DeviationAnalysisResponse, error) {
	reviews, err := s.phaseReviews.ListByAnalysisIDs(ctx, []uint{analysis.ID})
	if err != nil {
		return dto.DeviationAnalysisResponse{}, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to load phase reviews", err)
	}
	return dto.NewDeviationAnalysisResponse(analysis, reviews), nil
}
func (s *DeviationAnalysisService) Get(ctx context.Context, id uint) (dto.DeviationAnalysisResponse, error) {
	analysis, err := s.analyses.GetByID(ctx, id, true)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dto.DeviationAnalysisResponse{}, util.NotFound("deviation analysis")
		}
		return dto.DeviationAnalysisResponse{}, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to load deviation analysis", err)
	}
	return s.withReviews(ctx, analysis)
}
func (s *DeviationAnalysisService) List(
	ctx context.Context, query dto.DeviationAnalysisQuery,
) (dto.DeviationAnalysisListResponse, error) {
	analyses, total, err := s.analyses.List(ctx, query)
	if err != nil {
		return dto.DeviationAnalysisListResponse{}, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to list deviation analyses", err)
	}
	response := dto.DeviationAnalysisListResponse{
		Items: make([]dto.DeviationAnalysisResponse, 0, len(analyses)), Total: total, Page: query.Page, Size: query.PageSize,
	}
	ids := make([]uint, 0, len(analyses))
	for _, analysis := range analyses {
		ids = append(ids, analysis.ID)
	}
	reviews, err := s.phaseReviews.ListByAnalysisIDs(ctx, ids)
	if err != nil {
		return dto.DeviationAnalysisListResponse{}, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to load phase reviews", err)
	}
	for _, analysis := range analyses {
		response.Items = append(response.Items, dto.NewDeviationAnalysisResponse(analysis, reviews))
	}
	return response, nil
}
func (s *DeviationAnalysisService) Transition(
	ctx context.Context, id uint, request dto.DeviationAnalysisTransitionRequest, actor util.Actor,
) (dto.DeviationAnalysisResponse, error) {
	analysis, err := s.analyses.GetByID(ctx, id, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dto.DeviationAnalysisResponse{}, util.NotFound("deviation analysis")
		}
		return dto.DeviationAnalysisResponse{}, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to load deviation analysis", err)
	}
	from, to := constants.AnalysisState(analysis.AnalysisState), constants.AnalysisState(request.ToState)
	if !constants.CanTransitionAnalysis(from, to) {
		return dto.DeviationAnalysisResponse{}, util.NewError(http.StatusConflict, util.CodeStateTransition,
			"illegal analysis transition from "+analysis.AnalysisState+" to "+request.ToState)
	}
	if to == constants.AnalysisConfirmed && !analysis.ReviewerSeparated(actor.UserID) {
		return dto.DeviationAnalysisResponse{}, util.NewError(http.StatusConflict, util.CodeReviewerConflict,
			"analysis initiator cannot confirm their own result")
	}
	if to == constants.AnalysisConfirmed {
		if err := s.requirePhaseDispositions(ctx, analysis); err != nil {
			return dto.DeviationAnalysisResponse{}, err
		}
	}
	before := analysis
	updates := map[string]any{"review_comment": strings.TrimSpace(request.Comment)}
	if to == constants.AnalysisReviewed {
		updates["reviewed_by"] = actor.UserID
		updates["reviewed_by_name"] = actor.Username
	}
	changed, err := s.analyses.Transition(ctx, id, analysis.AnalysisState, request.ToState, updates)
	if err != nil {
		return dto.DeviationAnalysisResponse{}, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to transition deviation analysis", err)
	}
	if !changed {
		return dto.DeviationAnalysisResponse{}, util.NewError(http.StatusConflict, util.CodeConflict, "analysis state changed concurrently")
	}
	analysis.AnalysisState = request.ToState
	analysis.ReviewComment = strings.TrimSpace(request.Comment)
	if to == constants.AnalysisReviewed {
		analysis.ReviewedBy = &actor.UserID
		analysis.ReviewedByName = actor.Username
	}
	if err := recordAudit(ctx, s.audits, actor, "deviation_analysis", id, "transition", before, analysis,
		analysis.InputHash, analysis.AlgorithmVersion, 0); err != nil {
		return dto.DeviationAnalysisResponse{}, err
	}
	return s.Get(ctx, id)
}

// requirePhaseDispositions blocks confirmation while any abnormal phase still
// lacks a reviewer disposition, naming the outstanding phases in the error.
func (s *DeviationAnalysisService) requirePhaseDispositions(ctx context.Context, analysis model.DeviationAnalysis) error {
	abnormal, err := algorithm.AbnormalPhases(analysis.PhaseScoresJSON)
	if err != nil {
		return util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to decode phase evidence", err)
	}
	if len(abnormal) == 0 {
		return nil
	}
	reviews, err := s.phaseReviews.ListByAnalysisIDs(ctx, []uint{analysis.ID})
	if err != nil {
		return util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to load phase reviews", err)
	}
	disposed := make(map[string]bool, len(reviews))
	for _, review := range reviews {
		disposed[review.Phase] = true
	}
	missing := make([]string, 0, len(abnormal))
	for _, phase := range abnormal {
		if !disposed[phase] {
			missing = append(missing, phase)
		}
	}
	if len(missing) > 0 {
		return util.NewError(http.StatusConflict, util.CodeStateTransition,
			"phase review dispositions are still missing for: "+strings.Join(missing, ", "))
	}
	return nil
}

// SubmitPhaseReview records or revises the reviewer disposition for one
// abnormal phase. Dispositions freeze once the analysis leaves the open states.
func (s *DeviationAnalysisService) SubmitPhaseReview(
	ctx context.Context, id uint, request dto.SubmitPhaseReviewRequest, actor util.Actor,
) (dto.DeviationAnalysisResponse, error) {
	analysis, err := s.analyses.GetByID(ctx, id, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dto.DeviationAnalysisResponse{}, util.NotFound("deviation analysis")
		}
		return dto.DeviationAnalysisResponse{}, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to load deviation analysis", err)
	}
	if !constants.FermentationPhase(request.Phase).Valid() {
		return dto.DeviationAnalysisResponse{}, util.NewError(http.StatusBadRequest, util.CodeValidation, "unknown fermentation phase "+request.Phase)
	}
	if !constants.PhaseDisposition(request.Disposition).Valid() {
		return dto.DeviationAnalysisResponse{}, util.NewError(http.StatusBadRequest, util.CodeValidation, "unknown phase disposition "+request.Disposition)
	}
	note := strings.TrimSpace(request.Note)
	if note == "" {
		return dto.DeviationAnalysisResponse{}, util.NewError(http.StatusBadRequest, util.CodeValidation, "phase review note is required")
	}
	abnormal, err := algorithm.AbnormalPhases(analysis.PhaseScoresJSON)
	if err != nil {
		return dto.DeviationAnalysisResponse{}, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to decode phase evidence", err)
	}
	phaseNeedsReview := false
	for _, phase := range abnormal {
		if phase == request.Phase {
			phaseNeedsReview = true
			break
		}
	}
	if !phaseNeedsReview {
		return dto.DeviationAnalysisResponse{}, util.NewError(http.StatusConflict, util.CodeStateTransition,
			"phase "+request.Phase+" is not abnormal in this analysis and takes no disposition")
	}
	priorReviews, err := s.phaseReviews.ListByAnalysisIDs(ctx, []uint{id})
	if err != nil {
		return dto.DeviationAnalysisResponse{}, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to load phase reviews", err)
	}
	now := s.now()
	review := model.AnalysisPhaseReview{
		AnalysisID: id, Phase: request.Phase, Disposition: request.Disposition, Note: note,
		SubmittedBy: actor.UserID, SubmittedByName: actor.Username, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.phaseReviews.UpsertWhileOpen(ctx, review, phaseReviewOpenStates); err != nil {
		if errors.Is(err, repository.ErrPhaseReviewClosed) {
			return dto.DeviationAnalysisResponse{}, util.NewError(http.StatusConflict, util.CodeStateTransition,
				"analysis is "+analysis.AnalysisState+" and its phase review dispositions are locked")
		}
		return dto.DeviationAnalysisResponse{}, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to store phase review", err)
	}
	var before any
	for _, prior := range priorReviews {
		if prior.Phase == request.Phase {
			before = prior
			break
		}
	}
	if err := recordAudit(ctx, s.audits, actor, "deviation_analysis", id, "phase_review", before, review,
		analysis.InputHash, analysis.AlgorithmVersion, 0); err != nil {
		return dto.DeviationAnalysisResponse{}, err
	}
	return s.Get(ctx, id)
}
func (s *DeviationAnalysisService) Replay(
	ctx context.Context, id uint, actor util.Actor,
) (dto.DeviationAnalysisResponse, error) {
	analysis, err := s.analyses.GetByID(ctx, id, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dto.DeviationAnalysisResponse{}, util.NotFound("deviation analysis")
		}
		return dto.DeviationAnalysisResponse{}, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to load deviation analysis", err)
	}
	if analysis.AnalysisState == string(constants.AnalysisQueued) || analysis.AnalysisState == string(constants.AnalysisAnalyzing) ||
		analysis.AnalysisState == string(constants.AnalysisFailed) {
		return dto.DeviationAnalysisResponse{}, util.NewError(http.StatusConflict, util.CodeStateTransition, "only completed analysis results can be replayed")
	}
	snapshot, err := algorithm.DecodeSnapshot(analysis.InputSnapshot)
	if err != nil {
		return dto.DeviationAnalysisResponse{}, util.WrapError(http.StatusUnprocessableEntity, util.CodeValidation, "frozen analysis snapshot is invalid", err)
	}
	result, err := s.evaluator.Evaluate(snapshot)
	if err != nil {
		return dto.DeviationAnalysisResponse{}, util.WrapError(http.StatusUnprocessableEntity, util.CodeValidation, "analysis replay failed", err)
	}
	passed := result.PhaseScoresJSON == analysis.PhaseScoresJSON &&
		string(result.DeviationLevel) == analysis.DeviationLevel &&
		result.AlignedCurveJSON == analysis.AlignedCurveJSON &&
		result.SuspectedCausesJSON == analysis.SuspectedCausesJSON &&
		result.Explanation == analysis.Explanation
	if err := s.analyses.SetReplayVerified(ctx, id, passed); err != nil {
		return dto.DeviationAnalysisResponse{}, util.WrapError(http.StatusInternalServerError, util.CodeInternal, "unable to store replay evidence", err)
	}
	if err := recordAudit(ctx, s.audits, actor, "deviation_analysis", id, "replay", analysis,
		map[string]any{"replay_verified": passed}, analysis.InputHash, analysis.AlgorithmVersion, 0); err != nil {
		return dto.DeviationAnalysisResponse{}, err
	}
	if !passed {
		return dto.DeviationAnalysisResponse{}, util.NewError(http.StatusConflict, util.CodeConflict, "replay result differs from the frozen historical result")
	}
	return s.Get(ctx, id)
}
