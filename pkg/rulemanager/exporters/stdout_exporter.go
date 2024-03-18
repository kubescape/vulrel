package exporters

import (
	"node-agent/pkg/malwarescanner"
	"node-agent/pkg/ruleengine"
	"os"

	log "github.com/sirupsen/logrus"
)

type StdoutExporter struct {
	logger *log.Logger
}

func InitStdoutExporter(useStdout *bool) *StdoutExporter {
	if useStdout == nil {
		useStdout = new(bool)
		*useStdout = os.Getenv("STDOUT_ENABLED") != "false"
	}
	if !*useStdout {
		return nil
	}

	logger := log.New()
	logger.SetFormatter(&log.JSONFormatter{})
	logger.SetOutput(os.Stderr)

	return &StdoutExporter{
		logger: logger,
	}
}

func (exporter *StdoutExporter) SendRuleAlert(failedRule ruleengine.RuleFailure) {
	exporter.logger.WithFields(log.Fields{
		"severity": failedRule.Priority(),
		"message":  failedRule.Error(),
		"event":    failedRule.Event(),
	}).Error(failedRule.Name())
}

func (exporter *StdoutExporter) SendMalwareAlert(malwareDescription malwarescanner.MalwareDescription) {
	exporter.logger.WithFields(log.Fields{
		"severity":       10,
		"description":    malwareDescription.Description,
		"hash":           malwareDescription.Hash,
		"path":           malwareDescription.Path,
		"size":           malwareDescription.Size,
		"pod":            malwareDescription.PodName,
		"namespace":      malwareDescription.Namespace,
		"container":      malwareDescription.ContainerName,
		"containerID":    malwareDescription.ContainerID,
		"isPartOfImage":  malwareDescription.IsPartOfImage,
		"containerImage": malwareDescription.ContainerImage,
		"resource":       malwareDescription.Resource,
	}).Error(malwareDescription.Name)
}
