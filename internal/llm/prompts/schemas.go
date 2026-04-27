package prompts

var artifactClasses = []string{
	"bill_statement",
	"receipt_purchase",
	"school_family",
	"medical_health",
	"insurance_vehicle",
	"tax_finance",
	"travel_event",
	"identity_legal",
	"correspondence",
	"newsletter_promo",
	"photo_memory",
	"generic_document",
}

var factTypes = []string{
	"date",
	"due_date",
	"amount",
	"document_type",
	"payment_status",
	"is_payment_due",
	"amount_paid",
	"amount_due",
	"decision_reason",
	"vendor",
	"person",
	"organization",
	"policy_number",
	"account_number",
	"document_title",
	"requested_action",
	"appointment",
}

var relationTypes = []string{
	"duplicate_of",
	"supersedes",
	"updates_fact",
	"same_obligation_as",
	"supports",
	"contradicts",
	"related_to",
}

func ActionItemsSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"items"},
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"category", "title", "summary", "why_it_matters", "action_text",
						"artifact_ids", "evidence_snippets", "due_at", "confidence",
					},
					"properties": map[string]any{
						"category": map[string]any{
							"type": "string",
							"enum": []string{
								"needs_action", "bills_money", "school_family", "travel_events",
								"house_car", "documents_to_file", "interesting", "unverified",
							},
						},
						"title":          map[string]any{"type": "string"},
						"summary":        map[string]any{"type": "string"},
						"why_it_matters": map[string]any{"type": "string"},
						"action_text":    map[string]any{"type": "string"},
						"artifact_ids": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "string"},
						},
						"evidence_snippets": evidenceSnippetArraySchema(),
						"due_at":            map[string]any{"type": "string"},
						"confidence":        map[string]any{"type": "number"},
					},
				},
			},
		},
	}
}

func ArtifactFieldsSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"facts"},
		"properties": map[string]any{
			"facts": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"artifact_id", "fact_type", "value", "text_value",
						"evidence_id", "quote", "confidence",
					},
					"properties": map[string]any{
						"artifact_id": map[string]any{"type": "string"},
						"fact_type": map[string]any{
							"type": "string",
							"enum": factTypes,
						},
						"value":       map[string]any{"type": []string{"object", "array", "string", "number", "boolean"}},
						"text_value":  map[string]any{"type": "string"},
						"evidence_id": map[string]any{"type": "string"},
						"quote":       map[string]any{"type": "string"},
						"confidence":  map[string]any{"type": "number"},
					},
				},
			},
		},
	}
}

func ArtifactClassificationSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"artifact_id", "class", "confidence", "evidence_id", "quote"},
		"properties": map[string]any{
			"artifact_id": map[string]any{"type": "string"},
			"class": map[string]any{
				"type": "string",
				"enum": artifactClasses,
			},
			"confidence":  map[string]any{"type": "number"},
			"evidence_id": map[string]any{"type": "string"},
			"quote":       map[string]any{"type": "string"},
		},
	}
}

func ArtifactReconciliationSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"relations"},
		"properties": map[string]any{
			"relations": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"source_artifact_id", "target_artifact_id", "relation_type",
						"source_evidence_id", "target_evidence_id", "reason", "confidence",
					},
					"properties": map[string]any{
						"source_artifact_id": map[string]any{"type": "string"},
						"target_artifact_id": map[string]any{"type": "string"},
						"relation_type": map[string]any{
							"type": "string",
							"enum": relationTypes,
						},
						"source_evidence_id": map[string]any{"type": "string"},
						"target_evidence_id": map[string]any{"type": "string"},
						"reason":             map[string]any{"type": "string"},
						"confidence":         map[string]any{"type": "number"},
					},
				},
			},
		},
	}
}

func WeeklyBriefingSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"items"},
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"category", "title", "summary", "why_it_matters",
						"artifact_ids", "evidence_snippets", "confidence",
					},
					"properties": map[string]any{
						"category": map[string]any{
							"type": "string",
							"enum": []string{
								"obligation", "change", "review_document",
								"conflict", "duplicate", "low_confidence_note",
							},
						},
						"title":             map[string]any{"type": "string"},
						"summary":           map[string]any{"type": "string"},
						"why_it_matters":    map[string]any{"type": "string"},
						"artifact_ids":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"evidence_snippets": evidenceSnippetArraySchema(),
						"confidence":        map[string]any{"type": "number"},
					},
				},
			},
		},
	}
}

func evidenceSnippetArraySchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"artifact_id", "quote"},
			"properties": map[string]any{
				"artifact_id": map[string]any{"type": "string"},
				"quote":       map[string]any{"type": "string"},
			},
		},
	}
}
