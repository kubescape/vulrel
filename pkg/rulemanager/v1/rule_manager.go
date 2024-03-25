package rulemanager

import (
	"context"
	"errors"
	"fmt"
	"node-agent/pkg/config"
	"node-agent/pkg/k8sclient"
	"node-agent/pkg/ruleengine"
	"node-agent/pkg/rulemanager"
	"node-agent/pkg/rulemanager/exporters"
	"node-agent/pkg/utils"
	"time"

	"github.com/cenkalti/backoff/v4"
	"go.opentelemetry.io/otel"
	corev1 "k8s.io/api/core/v1"

	bindingcache "node-agent/pkg/rulebindingmanager"

	"node-agent/pkg/metricsmanager"
	"node-agent/pkg/ruleengine/objectcache"

	tracerrandomxtype "node-agent/pkg/ebpf/gadgets/randomx/types"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	containercollection "github.com/inspektor-gadget/inspektor-gadget/pkg/container-collection"
	tracercapabilitiestype "github.com/inspektor-gadget/inspektor-gadget/pkg/gadgets/trace/capabilities/types"
	tracerdnstype "github.com/inspektor-gadget/inspektor-gadget/pkg/gadgets/trace/dns/types"
	tracerexectype "github.com/inspektor-gadget/inspektor-gadget/pkg/gadgets/trace/exec/types"
	tracernetworktype "github.com/inspektor-gadget/inspektor-gadget/pkg/gadgets/trace/network/types"
	traceropentype "github.com/inspektor-gadget/inspektor-gadget/pkg/gadgets/trace/open/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"

	storageUtils "github.com/kubescape/storage/pkg/utils"
)

type RuleManager struct {
	cfg                      config.Config
	ctx                      context.Context
	containerMutexes         storageUtils.MapMutex[string]    // key is k8sContainerID
	trackedContainers        mapset.Set[string]               // key is k8sContainerID
	watchedContainerChannels maps.SafeMap[string, chan error] // key is ContainerID
	k8sClient                k8sclient.K8sClientInterface
	ruleBindingCache         bindingcache.RuleBindingCache
	objectCache              objectcache.ObjectCache
	exporter                 exporters.Exporter
	metrics                  metricsmanager.MetricsManager
	syscallPeekFunc          func(nsMountId uint64) ([]string, error)
}

var _ rulemanager.RuleManagerClient = (*RuleManager)(nil)

func CreateRuleManager(ctx context.Context, cfg config.Config, k8sClient k8sclient.K8sClientInterface, ruleBindingCache bindingcache.RuleBindingCache, objectCache objectcache.ObjectCache, exporter exporters.Exporter, metrics metricsmanager.MetricsManager) (*RuleManager, error) {
	return &RuleManager{
		cfg:               cfg,
		ctx:               ctx,
		k8sClient:         k8sClient,
		containerMutexes:  storageUtils.NewMapMutex[string](),
		trackedContainers: mapset.NewSet[string](),
		ruleBindingCache:  ruleBindingCache,
		objectCache:       objectCache,
		exporter:          exporter,
		metrics:           metrics,
	}, nil
}

func (rm *RuleManager) monitorContainer(ctx context.Context, container *containercollection.Container, watchedContainer *utils.WatchedContainerData) error {
	syscallTicker := time.NewTicker(15 * time.Second)
	var pod *corev1.Pod
	if err := backoff.Retry(func() error {
		p, err := rm.k8sClient.GetKubernetesClient().CoreV1().Pods(container.K8s.Namespace).Get(ctx, container.K8s.PodName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		pod = p
		return nil
	}, backoff.NewExponentialBackOff()); err != nil {
		logger.L().Ctx(ctx).Error("RuleManager - failed to get pod", helpers.Error(err),
			helpers.String("namespace", container.K8s.Namespace),
			helpers.String("name", container.K8s.PodName))
	}

	for {
		select {
		case <-syscallTicker.C:
			// get syscalls
			// @amit - this is the function that is being called
			fmt.Printf("pod name", pod.Name)
		case err := <-watchedContainer.SyncChannel:
			switch {
			case errors.Is(err, utils.ContainerHasTerminatedError):
				return nil
			}
		}
	}
}

func (rm *RuleManager) startRuleManager(ctx context.Context, container *containercollection.Container, k8sContainerID string) {
	ctx, span := otel.Tracer("").Start(ctx, "RuleManager.startRuleManager")
	defer span.End()

	syncChannel := make(chan error, 10)
	rm.watchedContainerChannels.Set(container.Runtime.ContainerID, syncChannel)

	watchedContainer := &utils.WatchedContainerData{
		ContainerID:    container.Runtime.ContainerID,
		SyncChannel:    syncChannel,
		K8sContainerID: k8sContainerID,
		NsMntId:        container.Mntns,
	}

	if err := rm.monitorContainer(ctx, container, watchedContainer); err != nil {
		logger.L().Info("ApplicationProfileManager - stop monitor on container", helpers.String("reason", err.Error()),
			helpers.Int("container index", watchedContainer.ContainerIndex),
			helpers.String("container ID", watchedContainer.ContainerID),
			helpers.String("k8s workload", watchedContainer.K8sContainerID))
	}

	rm.deleteResources(watchedContainer)
}

func (rm *RuleManager) deleteResources(watchedContainer *utils.WatchedContainerData) {
	// make sure we don't run deleteResources and saveProfile at the same time
	rm.containerMutexes.Lock(watchedContainer.K8sContainerID)
	defer rm.containerMutexes.Unlock(watchedContainer.K8sContainerID)

	// delete resources
	watchedContainer.UpdateDataTicker.Stop()
	rm.trackedContainers.Remove(watchedContainer.K8sContainerID)
	rm.watchedContainerChannels.Delete(watchedContainer.ContainerID)

	// clean cached k8s podSpec
	// clean cached rules
}

func (rm *RuleManager) waitForContainer(k8sContainerID string) error {
	return backoff.Retry(func() error {
		if rm.trackedContainers.Contains(k8sContainerID) {
			return nil
		}
		return fmt.Errorf("container %s not found", k8sContainerID)
	}, backoff.NewExponentialBackOff())
}

func (rm *RuleManager) ContainerCallback(notif containercollection.PubSubEvent) {
	k8sContainerID := utils.CreateK8sContainerID(notif.Container.K8s.Namespace, notif.Container.K8s.PodName, notif.Container.K8s.ContainerName)

	switch notif.Type {
	case containercollection.EventTypeAddContainer:
		if rm.watchedContainerChannels.Has(notif.Container.Runtime.ContainerID) {
			logger.L().Debug("container already exist in memory",
				helpers.String("container ID", notif.Container.Runtime.ContainerID),
				helpers.String("k8s workload", k8sContainerID))
			return
		}
		rm.trackedContainers.Add(k8sContainerID)
		go rm.startRuleManager(rm.ctx, notif.Container, k8sContainerID)
	case containercollection.EventTypeRemoveContainer:
		channel := rm.watchedContainerChannels.Get(notif.Container.Runtime.ContainerID)
		if channel != nil {
			channel <- utils.ContainerHasTerminatedError
		}
		rm.watchedContainerChannels.Delete(notif.Container.Runtime.ContainerID)
	}
}

func (rm *RuleManager) RegisterPeekFunc(peek func(mntns uint64) ([]string, error)) {
	rm.syscallPeekFunc = peek
}

func (rm *RuleManager) ReportCapability(k8sContainerID string, event tracercapabilitiestype.Event) {
	if err := rm.waitForContainer(k8sContainerID); err != nil {
		return
	}
	// process capability
}

func (rm *RuleManager) ReportFileExec(k8sContainerID string, event tracerexectype.Event) {
	// TODO: Do we need to wait for this?
	// if err := rm.waitForContainer(k8sContainerID); err != nil {
	// 	return
	// }

	// list exec rules
	rules := rm.ruleBindingCache.ListRulesForPod(event.GetNamespace(), event.GetPod())

	rm.processEvent(utils.ExecveEventType, &event, rules)
}

func (rm *RuleManager) ReportFileOpen(k8sContainerID string, event traceropentype.Event) {
	if err := rm.waitForContainer(k8sContainerID); err != nil {
		return
	}
	// process file open

}
func (rm *RuleManager) ReportNetworkEvent(k8sContainerID string, event tracernetworktype.Event) {
	// noop
}

func (rm *RuleManager) ReportDNSEvent(event tracerdnstype.Event) {
	// noop
}

func (rm *RuleManager) ReportRandomxEvent(k8sContainerID string, event tracerrandomxtype.Event) {
	// noop
}

func (rm *RuleManager) processEvent(eventType utils.EventType, event interface{}, rules []ruleengine.RuleEvaluator) {

	// process file exec
	for _, rule := range rules {
		if rule == nil {
			continue
		}

		if !isRelevant(rule.Requirements(), eventType) {
			continue
		}

		res := rule.ProcessEvent(eventType, event, rm.objectCache)
		if res != nil {
			logger.L().Info("RuleManager FAILED - rule alert", helpers.String("rule", rule.Name()))
			rm.exporter.SendRuleAlert(res)
			rm.metrics.ReportRuleAlert(rule.Name())
		} else {
			logger.L().Info("RuleManager PASSED - rule alert", helpers.String("rule", rule.Name()))
		}
		rm.metrics.ReportRuleProcessed(rule.Name())
	}
}

// check if the event type is relevant to the rule
func isRelevant(ruleSpec ruleengine.RuleSpec, eventType utils.EventType) bool {
	for _, i := range ruleSpec.RequiredEventTypes() {
		if i == eventType {
			return true
		}
	}
	return false
}
