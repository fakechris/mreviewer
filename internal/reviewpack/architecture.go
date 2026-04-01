package reviewpack

func architecturePack() CapabilityPack {
	return CapabilityPack{
		ID:    "architecture",
		Scope: "Review the change set for architectural regressions, boundary leaks, layering violations, and maintainability risks.",
		Rubric: []string{
			"Focus on coupling, ownership boundaries, abstractions, and long-term code health.",
			"Prefer issues that will make future change harder or system behavior less predictable.",
		},
		EvidenceRequirements: []string{
			"Point to the boundary or abstraction that is being crossed or weakened.",
			"Explain why the current shape will create future maintenance or correctness pressure.",
		},
		OutputSchema: []string{
			"title",
			"category",
			"claim",
			"severity",
			"confidence",
			"location",
			"evidence",
			"recommendation",
		},
		Prompt: "Act like a staff engineer reviewing architectural integrity. Avoid generic refactor requests; report issues with concrete structural evidence.",
	}
}
