package reviewpack

func securityPack() CapabilityPack {
	return CapabilityPack{
		ID:    "security",
		Scope: "Review the change set for newly introduced security risks and exploit paths. Focus on issues introduced by this diff, not historical debt.",
		Rubric: []string{
			"Find authorization, injection, secret handling, deserialization, and trust-boundary regressions.",
			"Prefer concrete exploitability over vague best-practice complaints.",
			"Call out only issues that are materially supported by the diff and surrounding context.",
		},
		EvidenceRequirements: []string{
			"Point to the exact changed path and line or hunk when possible.",
			"Explain the attacker-controlled input, dangerous sink, or broken authorization decision.",
			"State the impact if the issue is exploitable.",
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
		Standards: []string{"OWASP", "ASVS"},
		HardExclusions: []string{
			"Do not report historical security debt that predates this diff.",
			"Do not flag vague best-practice concerns without an attacker-controlled path or broken trust boundary.",
			"Do not treat test-only fixtures, mocks, or obviously non-production code as launch blockers unless the diff wires them into production execution.",
			"Do not report style-only crypto or auth complaints when the diff does not change the relevant trust boundary.",
		},
		ConfidenceGate:      0.85,
		NewIssuesOnly:       true,
		ExploitabilityFocus: "Prioritize attacker-controlled input, reachable dangerous sinks, broken authorization decisions, exposed secrets, and concrete escalation paths.",
		Prompt:              "Use OWASP and ASVS as your security review framing. Report only newly introduced security issues with enough evidence to justify reviewer trust.",
	}
}
