package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type FieldRequest struct {
	ArtifactID    string
	ArtifactType  string
	Title         string
	Class         string
	PromptVersion string
	Evidence      []Evidence
}

type FactReasoner interface {
	ExtractFacts(ctx context.Context, req FieldRequest) (GeneratedFactResponse, error)
}

type GeneratedFactResponse struct {
	Facts []GeneratedFact `json:"facts"`
}

type GeneratedFact struct {
	ArtifactID string          `json:"artifact_id"`
	FactType   string          `json:"fact_type"`
	Value      json.RawMessage `json:"value"`
	TextValue  string          `json:"text_value"`
	EvidenceID string          `json:"evidence_id"`
	Quote      string          `json:"quote"`
	Confidence float64         `json:"confidence"`
}

var (
	amountPattern      = regexp.MustCompile(`(?i)\$\s?\d{1,3}(?:,\d{3})*(?:\.\d{2})?|\$\s?\d+(?:\.\d{2})?`)
	amountDuePattern   = regexp.MustCompile(`(?i)\b(?:amount\s+due|balance\s+due|payment\s+due|total\s+due|due\s+amount)\s*[:\-]?\s*(\$\s?\d{1,3}(?:,\d{3})*(?:\.\d{2})?|\$\s?\d+(?:\.\d{2})?)`)
	amountPaidPattern  = regexp.MustCompile(`(?i)\b(?:total\s+paid|amount\s+paid|paid)\s*[:\-]?\s*(\$\s?\d{1,3}(?:,\d{3})*(?:\.\d{2})?|\$\s?\d+(?:\.\d{2})?)|\bpaid\s+with[^$\n]{0,80}(\$\s?\d{1,3}(?:,\d{3})*(?:\.\d{2})?|\$\s?\d+(?:\.\d{2})?)`)
	duePattern         = regexp.MustCompile(`(?i)\b(?:due|deadline|return by|pay by|before|by)\s+(?:on\s+)?([a-z]+day|jan(?:uary)?\.?\s+\d{1,2}(?:,\s*\d{4})?|feb(?:ruary)?\.?\s+\d{1,2}(?:,\s*\d{4})?|mar(?:ch)?\.?\s+\d{1,2}(?:,\s*\d{4})?|apr(?:il)?\.?\s+\d{1,2}(?:,\s*\d{4})?|may\s+\d{1,2}(?:,\s*\d{4})?|jun(?:e)?\.?\s+\d{1,2}(?:,\s*\d{4})?|jul(?:y)?\.?\s+\d{1,2}(?:,\s*\d{4})?|aug(?:ust)?\.?\s+\d{1,2}(?:,\s*\d{4})?|sep(?:tember)?\.?\s+\d{1,2}(?:,\s*\d{4})?|oct(?:ober)?\.?\s+\d{1,2}(?:,\s*\d{4})?|nov(?:ember)?\.?\s+\d{1,2}(?:,\s*\d{4})?|dec(?:ember)?\.?\s+\d{1,2}(?:,\s*\d{4})?|\d{1,2}/\d{1,2}(?:/\d{2,4})?|\d{4}-\d{2}-\d{2})\b`)
	datePattern        = regexp.MustCompile(`(?i)\b([a-z]+day|jan(?:uary)?\.?\s+\d{1,2}(?:,\s*\d{4})?|feb(?:ruary)?\.?\s+\d{1,2}(?:,\s*\d{4})?|mar(?:ch)?\.?\s+\d{1,2}(?:,\s*\d{4})?|apr(?:il)?\.?\s+\d{1,2}(?:,\s*\d{4})?|may\s+\d{1,2}(?:,\s*\d{4})?|jun(?:e)?\.?\s+\d{1,2}(?:,\s*\d{4})?|jul(?:y)?\.?\s+\d{1,2}(?:,\s*\d{4})?|aug(?:ust)?\.?\s+\d{1,2}(?:,\s*\d{4})?|sep(?:tember)?\.?\s+\d{1,2}(?:,\s*\d{4})?|oct(?:ober)?\.?\s+\d{1,2}(?:,\s*\d{4})?|nov(?:ember)?\.?\s+\d{1,2}(?:,\s*\d{4})?|dec(?:ember)?\.?\s+\d{1,2}(?:,\s*\d{4})?|\d{1,2}/\d{1,2}(?:/\d{2,4})?|\d{4}-\d{2}-\d{2})\b`)
	policyPattern      = regexp.MustCompile(`(?i)\b(?:policy|member|claim)\s*(?:number|no\.?|#)?\s*[:#]?\s*([A-Z0-9][A-Z0-9-]{4,})\b`)
	accountPattern     = regexp.MustCompile(`(?i)\b(?:account|acct)\s*(?:number|no\.?|#)?\s*[:#]?\s*([A-Z0-9][A-Z0-9-]{4,})\b`)
	actionPattern      = regexp.MustCompile(`(?i)\b(please\s+)?(submit|return|pay|sign|schedule|call|start|renew|complete|confirm|respond|bring|upload)\b[^.!?\n]{0,180}`)
	appointmentPattern = regexp.MustCompile(`(?i)\b(appointment|visit|consultation|checkup|meeting)\b[^.!?\n]{0,160}`)
	paidSignalPattern  = regexp.MustCompile(`(?i)\b(total\s+paid|amount\s+paid|paid\s+with|thank\s+you|receipt|payment\s+received|paid\s+in\s+full|paid)\b`)
	dueSignalPattern   = regexp.MustCompile(`(?i)\b(amount\s+due|balance\s+due|payment\s+due|payment\s+is\s+due|bill\s+is\s+due|invoice\s+is\s+due|pay\s+by|due\s+date|due\s+by|due\s+on|unpaid\s+invoice)\b`)
)

func RuleFacts(req FieldRequest) GeneratedFactResponse {
	var facts []GeneratedFact
	if strings.TrimSpace(req.Title) != "" {
		facts = append(facts, generatedRuleFact(req, FactDocumentTitle, req.Title, req.Title, 0.65))
	}

	seen := map[string]bool{}
	for _, item := range req.Evidence {
		text := item.Quote
		addMatches := func(factType string, pattern *regexp.Regexp, confidence float64) {
			for _, match := range pattern.FindAllStringSubmatch(text, 8) {
				value := strings.TrimSpace(match[0])
				if len(match) > 1 && strings.TrimSpace(match[1]) != "" && factType != FactRequestedAction && factType != FactAppointment {
					value = strings.TrimSpace(match[1])
				}
				key := factType + "\x00" + normalize(value)
				if value == "" || seen[key] {
					continue
				}
				seen[key] = true
				facts = append(facts, GeneratedFact{
					ArtifactID: req.ArtifactID,
					FactType:   factType,
					Value:      json.RawMessage(jsonValue(value)),
					TextValue:  value,
					EvidenceID: item.ID,
					Quote:      quoteAround(text, value),
					Confidence: confidence,
				})
			}
		}
		addMatches(FactAmount, amountPattern, 0.78)
		addMatches(FactDueDate, duePattern, 0.82)
		addMatches(FactDate, datePattern, 0.62)
		addMatches(FactPolicyNumber, policyPattern, 0.82)
		addMatches(FactAccountNumber, accountPattern, 0.82)
		addMatches(FactRequestedAction, actionPattern, 0.72)
		addMatches(FactAppointment, appointmentPattern, 0.72)
		addPaymentRuleFacts(req, item, seen, &facts)
	}

	return GeneratedFactResponse{Facts: facts}
}

func addPaymentRuleFacts(req FieldRequest, item Evidence, seen map[string]bool, facts *[]GeneratedFact) {
	text := item.Quote
	docType := inferDocumentType(req, item)
	if docType != "" {
		appendRuleFact(req, item, seen, facts, FactDocumentType, jsonObject("document_type", docType), docType, quoteForValue(text, docType), 0.72)
	}

	dueSignal := strings.TrimSpace(dueSignalPattern.FindString(text))
	paidSignal := strings.TrimSpace(paidSignalPattern.FindString(text))
	if dueSignal != "" {
		appendRuleFact(req, item, seen, facts, FactPaymentStatus, jsonObject("payment_status", "payment_due"), "payment_due", quoteForValue(text, dueSignal), 0.84)
		appendRuleFact(req, item, seen, facts, FactIsPaymentDue, json.RawMessage(`true`), "true", quoteForValue(text, dueSignal), 0.84)
		if amount := firstSubmatch(amountDuePattern, text); amount != "" {
			appendRuleFact(req, item, seen, facts, FactAmountDue, jsonObject("amount", amount, "currency", "USD"), amount, quoteForValue(text, amount), 0.86)
		}
		appendRuleFact(req, item, seen, facts, FactDecisionReason, json.RawMessage(jsonValue(fmt.Sprintf("%s indicates payment is due.", dueSignal))), fmt.Sprintf("%s indicates payment is due.", dueSignal), quoteForValue(text, dueSignal), 0.78)
		return
	}
	if paidSignal == "" {
		return
	}
	appendRuleFact(req, item, seen, facts, FactPaymentStatus, jsonObject("payment_status", "paid"), "paid", quoteForValue(text, paidSignal), 0.84)
	appendRuleFact(req, item, seen, facts, FactIsPaymentDue, json.RawMessage(`false`), "false", quoteForValue(text, paidSignal), 0.84)
	if amount := firstSubmatch(amountPaidPattern, text); amount != "" {
		appendRuleFact(req, item, seen, facts, FactAmountPaid, jsonObject("amount", amount, "currency", "USD"), amount, quoteForValue(text, amount), 0.86)
	}
	appendRuleFact(req, item, seen, facts, FactDecisionReason, json.RawMessage(jsonValue(fmt.Sprintf("%s indicates payment was already made.", paidSignal))), fmt.Sprintf("%s indicates payment was already made.", paidSignal), quoteForValue(text, paidSignal), 0.78)
}

func appendRuleFact(req FieldRequest, item Evidence, seen map[string]bool, facts *[]GeneratedFact, factType string, value json.RawMessage, textValue, quote string, confidence float64) {
	textValue = strings.TrimSpace(textValue)
	if textValue == "" {
		return
	}
	key := factType + "\x00" + normalize(textValue)
	if seen[key] {
		return
	}
	seen[key] = true
	*facts = append(*facts, GeneratedFact{
		ArtifactID: req.ArtifactID,
		FactType:   factType,
		Value:      value,
		TextValue:  textValue,
		EvidenceID: item.ID,
		Quote:      quote,
		Confidence: confidence,
	})
}

func inferDocumentType(req FieldRequest, item Evidence) string {
	haystack := normalize(req.Title + "\n" + item.Quote)
	switch {
	case strings.Contains(haystack, "receipt"):
		return "receipt"
	case strings.Contains(haystack, "invoice"):
		return "invoice"
	case strings.Contains(haystack, "estimate"):
		return "estimate"
	case strings.Contains(haystack, "statement"):
		return "statement"
	case strings.Contains(haystack, "bill"):
		return "bill"
	case strings.Contains(haystack, "appointment"):
		return "appointment_notice"
	}
	switch req.Class {
	case ClassReceiptPurchase:
		return "receipt"
	case ClassBillStatement:
		return "bill"
	case ClassMedicalHealth:
		return "medical_instruction"
	case ClassSchoolFamily:
		return "school_form"
	}
	return ""
}

func quoteForValue(text, value string) string {
	quote := quoteAround(text, value)
	if strings.TrimSpace(quote) != "" {
		return quote
	}
	return strings.TrimSpace(text)
}

func firstSubmatch(pattern *regexp.Regexp, text string) string {
	match := pattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	for _, value := range match[1:] {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func jsonObject(keyValues ...string) json.RawMessage {
	out := make(map[string]string, len(keyValues)/2)
	for i := 0; i+1 < len(keyValues); i += 2 {
		out[keyValues[i]] = keyValues[i+1]
	}
	data, err := json.Marshal(out)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

func ValidateGeneratedFacts(response GeneratedFactResponse, req FieldRequest, sourceType, modelName string) []Fact {
	if req.PromptVersion == "" {
		req.PromptVersion = fieldPromptVersion
	}
	evidenceByID := make(map[string]Evidence, len(req.Evidence))
	for _, item := range req.Evidence {
		evidenceByID[item.ID] = item
	}

	var out []Fact
	seen := map[string]bool{}
	for _, generated := range response.Facts {
		artifactID := strings.TrimSpace(generated.ArtifactID)
		if artifactID == "" {
			artifactID = req.ArtifactID
		}
		if artifactID != req.ArtifactID {
			continue
		}
		factType := strings.TrimSpace(generated.FactType)
		if !allowedFactTypes[factType] {
			continue
		}
		textValue := strings.TrimSpace(generated.TextValue)
		if textValue == "" {
			textValue = textValueFromJSON(generated.Value)
		}
		if textValue == "" {
			continue
		}
		var ok bool
		textValue, ok = normalizeFactValue(factType, textValue, generated.Value)
		if !ok {
			continue
		}
		valueJSON := strings.TrimSpace(string(generated.Value))
		if valueJSON == "" {
			valueJSON = jsonValue(textValue)
		}
		if !json.Valid([]byte(valueJSON)) {
			continue
		}
		if generated.Confidence < 0 || generated.Confidence > 1 {
			continue
		}

		evidenceID := strings.TrimSpace(generated.EvidenceID)
		quote := strings.TrimSpace(generated.Quote)
		if evidenceID != "" {
			if _, ok := evidenceByID[evidenceID]; !ok {
				continue
			}
		} else {
			var matched bool
			evidenceID, matched = matchEvidenceQuote(quote, req.Evidence)
			if !matched {
				continue
			}
		}

		key := factType + "\x00" + normalize(textValue) + "\x00" + evidenceID
		if seen[key] {
			continue
		}
		seen[key] = true
		hash := inputHash(req.ArtifactID, factType, textValue, evidenceID, sourceType, modelName, req.PromptVersion)
		out = append(out, Fact{
			ID:            hashID("fact", req.ArtifactID, factType, textValue, evidenceID, sourceType),
			ArtifactID:    req.ArtifactID,
			Type:          factType,
			ValueJSON:     valueJSON,
			TextValue:     normalize(textValue),
			EvidenceID:    evidenceID,
			Quote:         quote,
			Confidence:    generated.Confidence,
			SourceType:    sourceType,
			ModelName:     modelName,
			PromptVersion: req.PromptVersion,
			InputHash:     hash,
		})
	}
	return out
}

func ExtractFacts(ctx context.Context, db *sql.DB, artifactID string, reasoner FactReasoner, modelName string) ([]Fact, bool, error) {
	snapshot, ok, err := LoadArtifactSnapshot(ctx, db, artifactID)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, fmt.Errorf("artifact %s not found", artifactID)
	}
	evidence, err := ListEvidence(ctx, db, artifactID)
	if err != nil {
		return nil, false, err
	}
	classification, _, err := LoadClassification(ctx, db, artifactID)
	if err != nil {
		return nil, false, err
	}
	req := FieldRequest{
		ArtifactID:    artifactID,
		ArtifactType:  snapshot.Type,
		Title:         snapshot.Title,
		Class:         classification.Class,
		PromptVersion: fieldPromptVersion,
		Evidence:      evidence,
	}

	ruleFacts := ValidateGeneratedFacts(RuleFacts(req), req, SourceRule, "")
	if reasoner == nil {
		return ruleFacts, false, nil
	}
	generated, err := reasoner.ExtractFacts(ctx, req)
	if err != nil {
		return ruleFacts, true, err
	}
	llmFacts := ValidateGeneratedFacts(generated, req, SourceLLM, modelName)
	return mergeFacts(ruleFacts, llmFacts), true, nil
}

func generatedRuleFact(req FieldRequest, factType, value, quote string, confidence float64) GeneratedFact {
	evidenceID := ""
	if len(req.Evidence) > 0 {
		evidenceID = req.Evidence[0].ID
	}
	return GeneratedFact{
		ArtifactID: req.ArtifactID,
		FactType:   factType,
		Value:      json.RawMessage(jsonValue(value)),
		TextValue:  value,
		EvidenceID: evidenceID,
		Quote:      quote,
		Confidence: confidence,
	}
}

func quoteAround(text, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lowerText := strings.ToLower(text)
	index := strings.Index(lowerText, strings.ToLower(value))
	if index < 0 {
		return value
	}
	start := index - 80
	if start < 0 {
		start = 0
	}
	end := index + len(value) + 80
	if end > len(text) {
		end = len(text)
	}
	return strings.TrimSpace(text[start:end])
}

func matchEvidenceQuote(quote string, evidence []Evidence) (string, bool) {
	quote = normalize(quote)
	if quote == "" {
		return "", false
	}
	for _, item := range evidence {
		if strings.Contains(normalize(item.Quote), quote) {
			return item.ID, true
		}
	}
	return "", false
}

func textValueFromJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asBool bool
	if err := json.Unmarshal(raw, &asBool); err == nil {
		if asBool {
			return "true"
		}
		return "false"
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var asObject map[string]any
	if err := json.Unmarshal(raw, &asObject); err == nil {
		for _, key := range []string{
			"text", "value", "date", "amount", "amount_paid", "amount_due",
			"name", "document_type", "payment_status", "is_payment_due", "reason",
			"decision_reason",
		} {
			if value, ok := asObject[key]; ok {
				return strings.TrimSpace(fmt.Sprint(value))
			}
		}
	}
	return ""
}

func normalizeFactValue(factType, textValue string, raw json.RawMessage) (string, bool) {
	textValue = strings.TrimSpace(textValue)
	switch factType {
	case FactDocumentType:
		value := enumToken(textValue)
		return value, allowedDocumentTypes[value]
	case FactPaymentStatus:
		value := enumToken(textValue)
		return value, allowedPaymentStatuses[value]
	case FactIsPaymentDue:
		value := strings.ToLower(strings.TrimSpace(textValue))
		if value != "true" && value != "false" {
			var asBool bool
			if err := json.Unmarshal(raw, &asBool); err != nil {
				return "", false
			}
			if asBool {
				value = "true"
			} else {
				value = "false"
			}
		}
		return value, true
	default:
		return textValue, true
	}
}

func enumToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func mergeFacts(groups ...[]Fact) []Fact {
	seen := map[string]bool{}
	var out []Fact
	for _, facts := range groups {
		for _, fact := range facts {
			key := fact.Type + "\x00" + fact.TextValue
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, fact)
		}
	}
	return out
}
