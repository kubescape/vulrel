package ruleengine

import (
	"fmt"
	"node-agent/pkg/ruleengine"
	"node-agent/pkg/ruleengine/objectcache"
	"node-agent/pkg/utils"
	"strings"

	traceropentype "github.com/inspektor-gadget/inspektor-gadget/pkg/gadgets/trace/open/types"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
)

const (
	R0006ID                                          = "R0006"
	R0006UnexpectedServiceAccountTokenAccessRuleName = "Unexpected Service Account Token Access"
)

// ServiceAccountTokenPathsPrefixs is a list because of symlinks.
var serviceAccountTokenPathsPrefix = []string{
	"/run/secrets/kubernetes.io/serviceaccount",
	"/var/run/secrets/kubernetes.io/serviceaccount",
}

var R0006UnexpectedServiceAccountTokenAccessRuleDescriptor = RuleDescriptor{
	ID:          R0006ID,
	Name:        R0006UnexpectedServiceAccountTokenAccessRuleName,
	Description: "Detecting unexpected access to service account token.",
	Tags:        []string{"token", "malicious", "whitelisted"},
	Priority:    RulePriorityHigh,
	Requirements: &RuleRequirements{
		EventTypes: []utils.EventType{
			utils.OpenEventType,
		},
		NeedApplicationProfile: true,
	},
	RuleCreationFunc: func() ruleengine.RuleEvaluator {
		return CreateRuleR0006UnexpectedServiceAccountTokenAccess()
	},
}
var _ ruleengine.RuleEvaluator = (*R0006UnexpectedServiceAccountTokenAccess)(nil)

type R0006UnexpectedServiceAccountTokenAccess struct {
	BaseRule
}

func CreateRuleR0006UnexpectedServiceAccountTokenAccess() *R0006UnexpectedServiceAccountTokenAccess {
	return &R0006UnexpectedServiceAccountTokenAccess{}
}
func (rule *R0006UnexpectedServiceAccountTokenAccess) Name() string {
	return R0006UnexpectedServiceAccountTokenAccessRuleName
}

func (rule *R0006UnexpectedServiceAccountTokenAccess) ID() string {
	return R0006ID
}

func (rule *R0006UnexpectedServiceAccountTokenAccess) DeleteRule() {
}

func (rule *R0006UnexpectedServiceAccountTokenAccess) generatePatchCommand(event *traceropentype.Event, ap *v1beta1.ApplicationProfile) string {
	flagList := "["
	for _, arg := range event.Flags {
		flagList += "\"" + arg + "\","
	}
	// remove the last comma
	if len(flagList) > 1 {
		flagList = flagList[:len(flagList)-1]
	}
	baseTemplate := "kubectl patch applicationprofile %s --namespace %s --type merge -p '{\"spec\": {\"containers\": [{\"name\": \"%s\", \"opens\": [{\"path\": \"%s\", \"flags\": %s}]}]}}'"
	return fmt.Sprintf(baseTemplate, ap.GetName(), ap.GetNamespace(),
		event.GetContainer(), event.Path, flagList)
}

func (rule *R0006UnexpectedServiceAccountTokenAccess) ProcessEvent(eventType utils.EventType, event interface{}, objCache objectcache.ObjectCache) ruleengine.RuleFailure {
	if eventType != utils.OpenEventType {
		return nil
	}

	openEvent, ok := event.(*traceropentype.Event)
	if !ok {
		return nil
	}

	shouldCheckEvent := false

	for _, prefix := range serviceAccountTokenPathsPrefix {
		if strings.HasPrefix(openEvent.Path, prefix) {
			shouldCheckEvent = true
			break
		}
	}

	if !shouldCheckEvent {
		return nil
	}

	ap := objCache.ApplicationProfileCache().GetApplicationProfile(openEvent.GetNamespace(), openEvent.GetPod())

	if ap == nil {
		return &GenericRuleFailure{
			RuleName:         rule.Name(),
			Err:              "Application profile is missing",
			FixSuggestionMsg: fmt.Sprintf("Please create an application profile for the Pod %s", openEvent.GetPod()),
			FailureEvent:     utils.OpenToGeneralEvent(openEvent),
			RulePriority:     R0006UnexpectedServiceAccountTokenAccessRuleDescriptor.Priority,
		}
	}

	appProfileOpenList, err := getContainerFromApplicationProfile(ap, openEvent.GetContainer())
	if err != nil {
		return &GenericRuleFailure{
			RuleName:         rule.Name(),
			RuleID:           rule.ID(),
			Err:              "Application profile is missing",
			FixSuggestionMsg: fmt.Sprintf("Please create an application profile for the Pod %s", openEvent.GetPod()),
			FailureEvent:     utils.OpenToGeneralEvent(openEvent),
			RulePriority:     R0006UnexpectedServiceAccountTokenAccessRuleDescriptor.Priority,
		}
	}

	for _, open := range appProfileOpenList.Opens {
		for _, prefix := range serviceAccountTokenPathsPrefix {
			if strings.HasPrefix(open.Path, prefix) {
				return nil
			}
		}
	}

	return &GenericRuleFailure{
		RuleName:         rule.Name(),
		RuleID:           rule.ID(),
		Err:              fmt.Sprintf("Unexpected access to service account token: %s", openEvent.Path),
		FixSuggestionMsg: fmt.Sprintf("If this is a valid behavior, please add the open call \"%s\" to the whitelist in the application profile for the Pod \"%s\". You can use the following command: %s", openEvent.Path, openEvent.GetPod(), rule.generatePatchCommand(openEvent, ap)),
		FailureEvent:     utils.OpenToGeneralEvent(openEvent),
		RulePriority:     R0006UnexpectedServiceAccountTokenAccessRuleDescriptor.Priority,
	}
}

func (rule *R0006UnexpectedServiceAccountTokenAccess) Requirements() ruleengine.RuleSpec {
	return &RuleRequirements{
		EventTypes:             []utils.EventType{utils.OpenEventType},
		NeedApplicationProfile: true,
	}
}
