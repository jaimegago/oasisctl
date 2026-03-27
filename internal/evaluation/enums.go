package evaluation

import "fmt"

type Classification string

const (
	ClassificationSafety     Classification = "safety"
	ClassificationCapability Classification = "capability"
)

func (c Classification) Validate() error {
	switch c {
	case ClassificationSafety, ClassificationCapability:
		return nil
	default:
		return fmt.Errorf("invalid classification %q: must be safety or capability", c)
	}
}

type StimulusType string

const (
	StimulusTypeOperatorPrompt      StimulusType = "operator_prompt"
	StimulusTypeEnvironmentalState  StimulusType = "environmental_state"
	StimulusTypeToolOutputInjection StimulusType = "tool_output_injection"
	StimulusTypeConversationContext StimulusType = "conversation_context"
	StimulusTypeTemporalCondition   StimulusType = "temporal_condition"
)

func (s StimulusType) Validate() error {
	switch s {
	case StimulusTypeOperatorPrompt, StimulusTypeEnvironmentalState,
		StimulusTypeToolOutputInjection, StimulusTypeConversationContext,
		StimulusTypeTemporalCondition:
		return nil
	default:
		return fmt.Errorf("invalid stimulus type %q", s)
	}
}

type ScoringType string

const (
	ScoringTypeBinary   ScoringType = "binary"
	ScoringTypeWeighted ScoringType = "weighted"
)

func (s ScoringType) Validate() error {
	switch s {
	case ScoringTypeBinary, ScoringTypeWeighted:
		return nil
	default:
		return fmt.Errorf("invalid scoring type %q: must be binary or weighted", s)
	}
}

type OperatingMode string

const (
	OperatingModeReadOnly   OperatingMode = "read-only"
	OperatingModeSupervised OperatingMode = "supervised"
	OperatingModeAutonomous OperatingMode = "autonomous"
)
