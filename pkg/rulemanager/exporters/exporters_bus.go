package exporters

import (
	"node-agent/pkg/malwarescanner"
	"node-agent/pkg/ruleengine"
	"os"

	log "github.com/sirupsen/logrus"
)

type ExportersConfig struct {
	StdoutExporter           *bool               `mapstructure:"stdoutExporter"`
	AlertManagerExporterUrls []string            `mapstructure:"alertManagerExporterUrls"`
	SyslogExporter           string              `mapstructure:"syslogExporterURL"`
	CsvRuleExporterPath      string              `mapstructure:"CsvRuleExporterPath"`
	CsvMalwareExporterPath   string              `mapstructure:"CsvMalwareExporterPath"`
	HTTPExporterConfig       *HTTPExporterConfig `mapstructure:"httpExporterConfig"`
}

// This file will contain the single point of contact for all exporters,
// it will be used by the engine to send alerts to all exporters.
type ExporterBus struct {
	// Exporters is a list of all exporters.
	exporters []Exporter
}

// InitExporters initializes all exporters.
func InitExporters(exportersConfig ExportersConfig) *ExporterBus {
	exporters := []Exporter{}
	for _, url := range exportersConfig.AlertManagerExporterUrls {
		alertMan := InitAlertManagerExporter(url)
		if alertMan != nil {
			exporters = append(exporters, alertMan)
		}
	}
	stdoutExp := InitStdoutExporter(exportersConfig.StdoutExporter)
	if stdoutExp != nil {
		exporters = append(exporters, stdoutExp)
	}
	syslogExp := InitSyslogExporter(exportersConfig.SyslogExporter)
	if syslogExp != nil {
		exporters = append(exporters, syslogExp)
	}
	csvExp := InitCsvExporter(exportersConfig.CsvRuleExporterPath, exportersConfig.CsvMalwareExporterPath)
	if csvExp != nil {
		exporters = append(exporters, csvExp)
	}
	if exportersConfig.HTTPExporterConfig == nil {
		if httpURL := os.Getenv("HTTP_ENDPOINT_URL"); httpURL != "" {
			exportersConfig.HTTPExporterConfig = &HTTPExporterConfig{}
			exportersConfig.HTTPExporterConfig.URL = httpURL
		}
	}
	if exportersConfig.HTTPExporterConfig != nil {
		httpExp, err := InitHTTPExporter(*exportersConfig.HTTPExporterConfig)
		if err != nil {
			log.WithError(err).Error("failed to initialize HTTP exporter")
		}
		exporters = append(exporters, httpExp)
	}

	if len(exporters) == 0 {
		panic("no exporters were initialized")
	}
	log.Info("exporters initialized")

	return &ExporterBus{exporters: exporters}
}

func (e *ExporterBus) SendRuleAlert(failedRule ruleengine.RuleFailure) {
	for _, exporter := range e.exporters {
		exporter.SendRuleAlert(failedRule)
	}
}

func (e *ExporterBus) SendMalwareAlert(malwareDescription malwarescanner.MalwareDescription) {
	for _, exporter := range e.exporters {
		exporter.SendMalwareAlert(malwareDescription)
	}
}