package reviewpack

import "strings"

type CapabilityPack struct {
	ID                   string
	Scope                string
	Rubric               []string
	EvidenceRequirements []string
	OutputSchema         []string
	Standards            []string
	Prompt               string
	HardExclusions       []string
	ConfidenceGate       float64
	NewIssuesOnly        bool
	ExploitabilityFocus  string
}

func (p CapabilityPack) SystemPrompt() string {
	var sections []string
	if p.Scope != "" {
		sections = append(sections, "Scope:\n"+p.Scope)
	}
	if len(p.Rubric) > 0 {
		sections = append(sections, "Rubric:\n- "+strings.Join(p.Rubric, "\n- "))
	}
	if len(p.EvidenceRequirements) > 0 {
		sections = append(sections, "Evidence requirements:\n- "+strings.Join(p.EvidenceRequirements, "\n- "))
	}
	if len(p.OutputSchema) > 0 {
		sections = append(sections, "Output schema:\n- "+strings.Join(p.OutputSchema, "\n- "))
	}
	if len(p.Standards) > 0 {
		sections = append(sections, "Standards:\n- "+strings.Join(p.Standards, "\n- "))
	}
	if len(p.HardExclusions) > 0 {
		sections = append(sections, "Hard exclusions:\n- "+strings.Join(p.HardExclusions, "\n- "))
	}
	if p.ConfidenceGate > 0 {
		sections = append(sections, "Confidence gate:\nOnly emit findings at or above the configured reviewer confidence threshold.")
	}
	if p.NewIssuesOnly {
		sections = append(sections, "Change policy:\nReport only newly introduced issues from this change set.")
	}
	if p.ExploitabilityFocus != "" {
		sections = append(sections, "Exploitability framing:\n"+p.ExploitabilityFocus)
	}
	if p.Prompt != "" {
		sections = append(sections, "Reviewer guidance:\n"+p.Prompt)
	}
	return strings.Join(sections, "\n\n")
}

func DefaultPacks() []CapabilityPack {
	return []CapabilityPack{
		securityPack(),
		architecturePack(),
		databasePack(),
	}
}

func Lookup(id string) (CapabilityPack, bool) {
	for _, pack := range DefaultPacks() {
		if pack.ID == id {
			return pack, true
		}
	}
	return CapabilityPack{}, false
}
