package reviewstatus

type Stage string

const (
	StageLoadingTarget     Stage = "loading_target"
	StageAssemblingContext Stage = "assembling_context"
	StageRunningPacks      Stage = "running_packs"
	StageRunningAdvisor    Stage = "running_advisor"
	StagePublishing        Stage = "publishing"
	StageComparingExternal Stage = "comparing_external"
	StageComparingTargets  Stage = "comparing_targets"
	StageCompleted         Stage = "completed"
)

func (s Stage) Description() string {
	switch s {
	case StageLoadingTarget:
		return "AI review is loading the review target"
	case StageAssemblingContext:
		return "AI review is assembling context"
	case StageRunningPacks:
		return "AI review is running specialist reviewers"
	case StageRunningAdvisor:
		return "AI review is running a stronger second opinion"
	case StagePublishing:
		return "AI review is publishing review comments"
	case StageComparingExternal:
		return "AI review is comparing external reviewers"
	case StageComparingTargets:
		return "AI review is comparing multiple review targets"
	case StageCompleted:
		return "AI review is completing"
	default:
		return ""
	}
}
