package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"zora/internal/llm/prompts"
)

const (
	StageExtractArtifact   = "extract_artifact"
	StageClassifyArtifact  = "classify_artifact"
	StageExtractFields     = "extract_fields"
	StageReconcileArtifact = "reconcile_artifact"
	StageGenerateBriefing  = "generate_briefing"

	SourceRule = "rule"
	SourceLLM  = "llm"

	ClassBillStatement    = "bill_statement"
	ClassReceiptPurchase  = "receipt_purchase"
	ClassSchoolFamily     = "school_family"
	ClassMedicalHealth    = "medical_health"
	ClassInsuranceVehicle = "insurance_vehicle"
	ClassTaxFinance       = "tax_finance"
	ClassTravelEvent      = "travel_event"
	ClassIdentityLegal    = "identity_legal"
	ClassCorrespondence   = "correspondence"
	ClassNewsletterPromo  = "newsletter_promo"
	ClassPhotoMemory      = "photo_memory"
	ClassGenericDocument  = "generic_document"

	FactDate            = "date"
	FactDueDate         = "due_date"
	FactAmount          = "amount"
	FactDocumentType    = "document_type"
	FactPaymentStatus   = "payment_status"
	FactIsPaymentDue    = "is_payment_due"
	FactAmountPaid      = "amount_paid"
	FactAmountDue       = "amount_due"
	FactDecisionReason  = "decision_reason"
	FactVendor          = "vendor"
	FactPerson          = "person"
	FactOrganization    = "organization"
	FactPolicyNumber    = "policy_number"
	FactAccountNumber   = "account_number"
	FactDocumentTitle   = "document_title"
	FactRequestedAction = "requested_action"
	FactAppointment     = "appointment"

	RelationDuplicateOf      = "duplicate_of"
	RelationSupersedes       = "supersedes"
	RelationUpdatesFact      = "updates_fact"
	RelationSameObligationAs = "same_obligation_as"
	RelationSupports         = "supports"
	RelationContradicts      = "contradicts"
	RelationRelatedTo        = "related_to"

	StatusProposed = "proposed"
)

const (
	fieldPromptVersion = prompts.ArtifactFieldsID
	maxEvidenceQuote   = 1200
)

var allowedClasses = map[string]bool{
	ClassBillStatement:    true,
	ClassReceiptPurchase:  true,
	ClassSchoolFamily:     true,
	ClassMedicalHealth:    true,
	ClassInsuranceVehicle: true,
	ClassTaxFinance:       true,
	ClassTravelEvent:      true,
	ClassIdentityLegal:    true,
	ClassCorrespondence:   true,
	ClassNewsletterPromo:  true,
	ClassPhotoMemory:      true,
	ClassGenericDocument:  true,
}

var allowedFactTypes = map[string]bool{
	FactDate:            true,
	FactDueDate:         true,
	FactAmount:          true,
	FactDocumentType:    true,
	FactPaymentStatus:   true,
	FactIsPaymentDue:    true,
	FactAmountPaid:      true,
	FactAmountDue:       true,
	FactDecisionReason:  true,
	FactVendor:          true,
	FactPerson:          true,
	FactOrganization:    true,
	FactPolicyNumber:    true,
	FactAccountNumber:   true,
	FactDocumentTitle:   true,
	FactRequestedAction: true,
	FactAppointment:     true,
}

var allowedRelationTypes = map[string]bool{
	RelationDuplicateOf:      true,
	RelationSupersedes:       true,
	RelationUpdatesFact:      true,
	RelationSameObligationAs: true,
	RelationSupports:         true,
	RelationContradicts:      true,
	RelationRelatedTo:        true,
}

var allowedDocumentTypes = map[string]bool{
	"receipt":             true,
	"bill":                true,
	"invoice":             true,
	"statement":           true,
	"estimate":            true,
	"medical_instruction": true,
	"school_form":         true,
	"appointment_notice":  true,
	"other":               true,
}

var allowedPaymentStatuses = map[string]bool{
	"paid":          true,
	"payment_due":   true,
	"refund_due":    true,
	"informational": true,
	"unknown":       true,
}

type Evidence struct {
	ID             string
	ArtifactID     string
	ChunkID        string
	Kind           string
	Quote          string
	CharStart      int
	CharEnd        int
	PageStart      int
	PageEnd        int
	Extractor      string
	ProvenanceJSON string
	CreatedAt      string
}

type Classification struct {
	ArtifactID    string
	Class         string
	EvidenceID    string
	Confidence    float64
	SourceType    string
	ModelName     string
	PromptVersion string
	InputHash     string
}

type Fact struct {
	ID            string
	ArtifactID    string
	Type          string
	ValueJSON     string
	TextValue     string
	EvidenceID    string
	Quote         string
	Confidence    float64
	SourceType    string
	ModelName     string
	PromptVersion string
	InputHash     string
}

type Relation struct {
	ID               string
	ProposalID       string
	SourceArtifactID string
	TargetArtifactID string
	Type             string
	SourceEvidenceID string
	TargetEvidenceID string
	Reason           string
	Confidence       float64
	Status           string
}

type StageResult struct {
	Stage  string `json:"stage"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type ProcessResult struct {
	ArtifactID        string        `json:"artifact_id"`
	Stages            []StageResult `json:"stages"`
	EvidenceCount     int           `json:"evidence_count"`
	Classification    string        `json:"classification"`
	FactCount         int           `json:"fact_count"`
	RelationCount     int           `json:"relation_count"`
	Warnings          []string      `json:"warnings,omitempty"`
	FieldLLMAttempted bool          `json:"field_llm_attempted"`
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func hashID(prefix string, parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "_" + hex.EncodeToString(hash[:])[:24]
}

func inputHash(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:])
}

func jsonValue(value string) string {
	data, err := json.Marshal(map[string]string{"text": strings.TrimSpace(value)})
	if err != nil {
		return `{"text":""}`
	}
	return string(data)
}

func normalize(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	if limit <= 16 {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-16])) + "\n[truncated]"
}
