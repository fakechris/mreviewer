package reviewpack

func databasePack() CapabilityPack {
	return CapabilityPack{
		ID:    "database",
		Scope: "Review the change set for schema, query, transaction, and data consistency risks.",
		Rubric: []string{
			"Look for unsafe migrations, broken transactional semantics, and inefficient query patterns introduced by the diff.",
			"Focus on correctness and operational safety before style.",
		},
		EvidenceRequirements: []string{
			"Point to the exact query, migration, or transaction boundary involved.",
			"Explain the data integrity, lock contention, or performance risk introduced.",
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
		Prompt: "Act like a database-focused reviewer. Prefer high-signal issues involving data correctness, operational safety, and migration risk.",
	}
}
