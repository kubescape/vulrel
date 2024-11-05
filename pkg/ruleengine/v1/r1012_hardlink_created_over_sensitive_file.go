package ruleengine

import (
	"fmt"
	"strings"

	"github.com/kubescape/node-agent/pkg/objectcache"
	"github.com/kubescape/node-agent/pkg/ruleengine"
	"github.com/kubescape/node-agent/pkg/utils"

	apitypes "github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"

	tracerhardlinktype "github.com/kubescape/node-agent/pkg/ebpf/gadgets/hardlink/types"
)

const (
	R1012ID   = "R1012"
	R1012Name = "Hardlink Created Over Sensitive File"
)

var R1012HardlinkCreatedOverSensitiveFileRuleDescriptor = ruleengine.RuleDescriptor{
	ID:          R1012ID,
	Name:        R1012Name,
	Description: "Detecting hardlink creation over sensitive files.",
	Tags:        []string{"files", "malicious"},
	Priority:    RulePriorityHigh,
	Requirements: &RuleRequirements{
		EventTypes: []utils.EventType{
			utils.HardlinkEventType,
		},
	},
	RuleCreationFunc: func() ruleengine.RuleEvaluator {
		return CreateRuleR1012HardlinkCreatedOverSensitiveFile()
	},
}
var _ ruleengine.RuleEvaluator = (*R1012HardlinkCreatedOverSensitiveFile)(nil)

type R1012HardlinkCreatedOverSensitiveFile struct {
	BaseRule
	additionalPaths []string
}

func CreateRuleR1012HardlinkCreatedOverSensitiveFile() *R1012HardlinkCreatedOverSensitiveFile {
	return &R1012HardlinkCreatedOverSensitiveFile{
		additionalPaths: SensitiveFiles,
	}
}

func (rule *R1012HardlinkCreatedOverSensitiveFile) SetParameters(parameters map[string]interface{}) {
	rule.BaseRule.SetParameters(parameters)

	additionalPathsInterface := rule.GetParameters()["additionalPaths"]
	if additionalPathsInterface == nil {
		return
	}

	additionalPaths, ok := interfaceToStringSlice(additionalPathsInterface)
	if ok {
		for _, path := range additionalPaths {
			rule.additionalPaths = append(rule.additionalPaths, fmt.Sprintf("%v", path))
		}
	} else {
		logger.L().Warning("failed to convert additionalPaths to []string", helpers.String("ruleID", rule.ID()))
	}
}

func (rule *R1012HardlinkCreatedOverSensitiveFile) Name() string {
	return R1012Name
}

func (rule *R1012HardlinkCreatedOverSensitiveFile) ID() string {
	return R1012ID
}

func (rule *R1012HardlinkCreatedOverSensitiveFile) DeleteRule() {
}

func (rule *R1012HardlinkCreatedOverSensitiveFile) ProcessEvent(eventType utils.EventType, event utils.K8sEvent, objCache objectcache.ObjectCache) ruleengine.RuleFailure {

	if !rule.EvaluateRule(eventType, event, objCache.K8sObjectCache()) {
		return nil
	}

	hardlinkEvent, _ := event.(*tracerhardlinktype.Event)

	if allowed, err := isAllowed(&hardlinkEvent.Event, objCache, hardlinkEvent.Comm, R1012ID); err != nil {
		logger.L().Error("failed to check if hardlink is allowed", helpers.String("ruleID", rule.ID()), helpers.String("error", err.Error()))
	} else if allowed {
		return nil
	}

	for _, path := range rule.additionalPaths {
		if strings.HasPrefix(hardlinkEvent.OldPath, path) {
			return &GenericRuleFailure{
				BaseRuntimeAlert: apitypes.BaseRuntimeAlert{
					AlertName: rule.Name(),
					Arguments: map[string]interface{}{
						"oldPath": hardlinkEvent.OldPath,
						"newPath": hardlinkEvent.NewPath,
					},
					InfectedPID:    hardlinkEvent.Pid,
					FixSuggestions: "If this is a legitimate action, please consider removing this workload from the binding of this rule.",
					Severity:       R1012HardlinkCreatedOverSensitiveFileRuleDescriptor.Priority,
				},
				RuntimeProcessDetails: apitypes.ProcessTree{
					ProcessTree: apitypes.Process{
						Comm:       hardlinkEvent.Comm,
						PPID:       hardlinkEvent.PPid,
						PID:        hardlinkEvent.Pid,
						UpperLayer: &hardlinkEvent.UpperLayer,
						Uid:        &hardlinkEvent.Uid,
						Gid:        &hardlinkEvent.Gid,
						Path:       hardlinkEvent.ExePath,
						Hardlink:   hardlinkEvent.ExePath,
					},
					ContainerID: hardlinkEvent.Runtime.ContainerID,
				},
				TriggerEvent: hardlinkEvent.Event,
				RuleAlert: apitypes.RuleAlert{
					RuleDescription: fmt.Sprintf("Hardlink created over sensitive file: %s - %s in: %s", hardlinkEvent.OldPath, hardlinkEvent.NewPath, hardlinkEvent.GetContainer()),
				},
				RuntimeAlertK8sDetails: apitypes.RuntimeAlertK8sDetails{
					PodName:   hardlinkEvent.GetPod(),
					PodLabels: hardlinkEvent.K8s.PodLabels,
				},
				RuleID: rule.ID(),
			}
		}
	}

	return nil
}

func (rule *R1012HardlinkCreatedOverSensitiveFile) EvaluateRule(eventType utils.EventType, event utils.K8sEvent, _ objectcache.K8sObjectCache) bool {
	if eventType != utils.HardlinkEventType {
		return false
	}
	_, ok := event.(*tracerhardlinktype.Event)
	return ok
}

func (rule *R1012HardlinkCreatedOverSensitiveFile) Requirements() ruleengine.RuleSpec {
	return &RuleRequirements{
		EventTypes: R1012HardlinkCreatedOverSensitiveFileRuleDescriptor.Requirements.RequiredEventTypes(),
	}
}
