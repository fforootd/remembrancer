package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

type Runner struct {
	DB                   *sql.DB
	FieldReasoner        FactReasoner
	FieldModelName       string
	IgnoreReasonerErrors bool
	Logger               *slog.Logger
	Now                  func() time.Time
}

func (r Runner) ProcessArtifact(ctx context.Context, artifactID string) (ProcessResult, error) {
	if r.DB == nil {
		return ProcessResult{}, fmt.Errorf("pipeline database is required")
	}
	now := r.now()
	result := ProcessResult{ArtifactID: artifactID}

	snapshot, ok, err := LoadArtifactSnapshot(ctx, r.DB, artifactID)
	if err != nil {
		return result, err
	}
	if !ok {
		return result, fmt.Errorf("artifact %s not found", artifactID)
	}
	extractHash := inputHash(snapshot.ID, snapshot.ContentHash, snapshot.Text)
	if err := MarkStageSucceeded(ctx, r.DB, artifactID, StageExtractArtifact, extractHash, now); err != nil {
		return result, err
	}
	result.Stages = append(result.Stages, StageResult{Stage: StageExtractArtifact, Status: "succeeded"})

	evidence, err := r.runEvidenceAndClassify(ctx, artifactID, snapshot, now)
	if err != nil {
		return result, err
	}
	result.EvidenceCount = len(evidence)
	result.Stages = append(result.Stages, StageResult{Stage: StageClassifyArtifact, Status: "succeeded"})

	facts, llmAttempted, err := r.runFactExtraction(ctx, artifactID, now)
	result.FieldLLMAttempted = llmAttempted
	if err != nil {
		return result, err
	} else {
		result.Stages = append(result.Stages, StageResult{Stage: StageExtractFields, Status: "succeeded"})
	}
	result.FactCount = len(facts)

	relations, err := r.runReconcile(ctx, artifactID, now)
	if err != nil {
		return result, err
	}
	result.RelationCount = len(relations)
	result.Stages = append(result.Stages, StageResult{Stage: StageReconcileArtifact, Status: "succeeded"})

	classification, _, err := LoadClassification(ctx, r.DB, artifactID)
	if err != nil {
		return result, err
	}
	result.Classification = classification.Class
	return result, nil
}

func (r Runner) runEvidenceAndClassify(ctx context.Context, artifactID string, snapshot artifactSnapshot, now time.Time) ([]Evidence, error) {
	stageHash := inputHash(snapshot.ID, snapshot.ContentHash, snapshot.Text)
	if err := MarkStageRunning(ctx, r.DB, artifactID, StageClassifyArtifact, stageHash, now); err != nil {
		return nil, err
	}
	evidence, err := UpsertEvidenceForArtifact(ctx, r.DB, artifactID, now)
	if err != nil {
		_ = MarkStageFailed(ctx, r.DB, artifactID, StageClassifyArtifact, stageHash, err, now)
		return nil, err
	}
	if _, err := ClassifyArtifact(ctx, r.DB, artifactID, evidence, now); err != nil {
		_ = MarkStageFailed(ctx, r.DB, artifactID, StageClassifyArtifact, stageHash, err, now)
		return nil, err
	}
	if err := MarkStageSucceeded(ctx, r.DB, artifactID, StageClassifyArtifact, stageHash, now); err != nil {
		return nil, err
	}
	return evidence, nil
}

func (r Runner) runFactExtraction(ctx context.Context, artifactID string, now time.Time) ([]Fact, bool, error) {
	stageHash := inputHash(artifactID, fieldPromptVersion, r.FieldModelName)
	if err := MarkStageRunning(ctx, r.DB, artifactID, StageExtractFields, stageHash, now); err != nil {
		return nil, false, err
	}
	facts, llmAttempted, err := ExtractFacts(ctx, r.DB, artifactID, r.FieldReasoner, r.FieldModelName)
	if err != nil && !r.IgnoreReasonerErrors {
		_ = MarkStageFailed(ctx, r.DB, artifactID, StageExtractFields, stageHash, err, now)
		return nil, llmAttempted, err
	}
	if err != nil && r.IgnoreReasonerErrors {
		r.logger().Warn("pipeline field extraction LLM failed; using rule facts", "artifact_id", artifactID, "error", err)
		facts, _, _ = ExtractFacts(ctx, r.DB, artifactID, nil, "")
		err = nil
	}
	if storeErr := ReplaceFacts(ctx, r.DB, artifactID, facts, now); storeErr != nil {
		_ = MarkStageFailed(ctx, r.DB, artifactID, StageExtractFields, stageHash, storeErr, now)
		return nil, llmAttempted, storeErr
	}
	if stageErr := MarkStageSucceeded(ctx, r.DB, artifactID, StageExtractFields, stageHash, now); stageErr != nil {
		return nil, llmAttempted, stageErr
	}
	return facts, llmAttempted, err
}

func (r Runner) runReconcile(ctx context.Context, artifactID string, now time.Time) ([]Relation, error) {
	stageHash := inputHash(artifactID, "relations")
	if err := MarkStageRunning(ctx, r.DB, artifactID, StageReconcileArtifact, stageHash, now); err != nil {
		return nil, err
	}
	relations, err := ReconcileArtifact(ctx, r.DB, artifactID, now)
	if err != nil {
		_ = MarkStageFailed(ctx, r.DB, artifactID, StageReconcileArtifact, stageHash, err, now)
		return nil, err
	}
	if err := MarkStageSucceeded(ctx, r.DB, artifactID, StageReconcileArtifact, stageHash, now); err != nil {
		return nil, err
	}
	return relations, nil
}

func (r Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r Runner) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}
