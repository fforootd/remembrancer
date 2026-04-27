package prompts

import (
	"bytes"
	"fmt"
	"sort"
	"text/template"
)

const (
	ActionItemsID            = "action-items-v1"
	ArtifactFieldsID         = "artifact-fields-v1"
	ArtifactClassificationID = "artifact-classification-v1"
	ArtifactReconciliationID = "artifact-reconciliation-v1"
	WeeklyBriefingID         = "weekly-briefing-v1"
)

type PromptSpec struct {
	ID           string
	System       string
	UserTemplate string
	Schema       map[string]any
}

func (p PromptSpec) RenderUser(data any) (string, error) {
	if p.ID == "" {
		return "", fmt.Errorf("prompt id is required")
	}
	tpl, err := template.New(p.ID).Option("missingkey=error").Parse(p.UserTemplate)
	if err != nil {
		return "", fmt.Errorf("parse prompt %s template: %w", p.ID, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render prompt %s: %w", p.ID, err)
	}
	return buf.String(), nil
}

func Get(id string) (PromptSpec, bool) {
	for _, spec := range All() {
		if spec.ID == id {
			return spec, true
		}
	}
	return PromptSpec{}, false
}

func MustGet(id string) PromptSpec {
	spec, ok := Get(id)
	if !ok {
		panic("unknown prompt: " + id)
	}
	return spec
}

func All() []PromptSpec {
	return []PromptSpec{
		ActionItems(),
		ArtifactFields(),
		ArtifactClassification(),
		ArtifactReconciliation(),
		WeeklyBriefing(),
	}
}

func IDs() []string {
	specs := All()
	ids := make([]string, 0, len(specs))
	for _, spec := range specs {
		ids = append(ids, spec.ID)
	}
	sort.Strings(ids)
	return ids
}
