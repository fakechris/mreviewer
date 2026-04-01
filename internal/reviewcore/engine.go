package reviewcore

import (
	"context"
	"fmt"

	"github.com/mreviewer/mreviewer/internal/reviewpack"
)

type PackRunner interface {
	Run(ctx context.Context, input ReviewInput, pack reviewpack.CapabilityPack) (ReviewerArtifact, error)
}

type Judge interface {
	Decide(target ReviewTarget, artifacts []ReviewerArtifact) ReviewBundle
}

type Engine struct {
	packs  map[string]reviewpack.CapabilityPack
	runner PackRunner
	judge  Judge
}

func NewEngine(packs []reviewpack.CapabilityPack, runner PackRunner, judge Judge) *Engine {
	indexed := make(map[string]reviewpack.CapabilityPack, len(packs))
	for _, pack := range packs {
		indexed[pack.ID] = pack
	}
	return &Engine{
		packs:  indexed,
		runner: runner,
		judge:  judge,
	}
}

func (e *Engine) Run(ctx context.Context, input ReviewInput, selectedPackIDs []string) (ReviewBundle, error) {
	if e == nil || e.runner == nil {
		return ReviewBundle{}, fmt.Errorf("review engine: pack runner is required")
	}
	if e.judge == nil {
		return ReviewBundle{}, fmt.Errorf("review engine: judge is required")
	}

	packIDs := selectedPackIDs
	if len(packIDs) == 0 {
		packIDs = make([]string, 0, len(e.packs))
		for id := range e.packs {
			packIDs = append(packIDs, id)
		}
	}

	artifacts := make([]ReviewerArtifact, 0, len(packIDs))
	for _, packID := range packIDs {
		pack, ok := e.packs[packID]
		if !ok {
			return ReviewBundle{}, fmt.Errorf("review engine: unknown pack %q", packID)
		}
		artifact, err := e.runner.Run(ctx, input, pack)
		if err != nil {
			return ReviewBundle{}, fmt.Errorf("review engine: run pack %q: %w", packID, err)
		}
		artifacts = append(artifacts, artifact)
	}

	return e.judge.Decide(input.Target, artifacts), nil
}
