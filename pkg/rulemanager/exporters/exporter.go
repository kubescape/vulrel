package exporters

import (
	"node-agent/pkg/malwarescanner"
	"node-agent/pkg/ruleengine"
)

// generic exporter interface
type Exporter interface {
	// SendRuleAlert sends an alert on failed rule to the exporter
	SendRuleAlert(failedRule ruleengine.RuleFailure)
	// SendMalwareAlert sends an alert on malware detection to the exporter.
	SendMalwareAlert(malwarescanner.MalwareDescription)
}

var _ Exporter = (*ExporterMock)(nil)

type ExporterMock struct{}

func (e *ExporterMock) SendRuleAlert(failedRule ruleengine.RuleFailure) {
}

func (e *ExporterMock) SendMalwareAlert(malwareDescription malwarescanner.MalwareDescription) {
}