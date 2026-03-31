package reviewcore

import (
	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/rules"
)

type ReviewInput struct {
	Target          ReviewTarget          `json:"target"`
	Request         ctxpkg.ReviewRequest  `json:"request"`
	EffectivePolicy rules.EffectivePolicy `json:"effective_policy"`
	Warnings        []string              `json:"warnings,omitempty"`
}
