package networkmanager

import (
	"context"
	"errors"
	"fmt"
	"node-agent/pkg/config"
	"node-agent/pkg/k8sclient"
	"node-agent/pkg/storage"
	"node-agent/pkg/utils"
	"time"

	"github.com/armosec/utils-k8s-go/wlid"
	"github.com/goradd/maps"
	containercollection "github.com/inspektor-gadget/inspektor-gadget/pkg/container-collection"
	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/k8s-interface/k8sinterface"
	"github.com/kubescape/k8s-interface/workloadinterface"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type NetworkManager struct {
	cfg                        config.Config
	ctx                        context.Context
	k8sClient                  k8sclient.K8sClientInterface
	storageClient              storage.StorageClient
	containerAndPodToWLIDMap   maps.SafeMap[string, string]
	containerAndPodToEventsMap maps.SafeMap[string, []*NetworkEvent]
	clusterName                string
	watchedContainerChannels   maps.SafeMap[string, chan error] // key is ContainerID
}

type Destination struct {
	Namespace    string
	Name         string
	EndpointKind string
	PodLabels    map[string]string
	IPAddress    string
}

type NetworkEvent struct {
	Port        uint16
	Protocol    string
	PodLabels   map[string]string
	Destination Destination
}

var _ NetworkManagerClient = (*NetworkManager)(nil)

func CreateNetworkManager(ctx context.Context, cfg config.Config, k8sClient k8sclient.K8sClientInterface, storageClient storage.StorageClient, clusterName string) (*NetworkManager, error) {
	return &NetworkManager{
		cfg:           cfg,
		ctx:           ctx,
		k8sClient:     k8sClient,
		storageClient: storageClient,
		clusterName:   clusterName,
	}, nil
}

func (am *NetworkManager) ContainerCallback(notif containercollection.PubSubEvent) {
	k8sContainerID := utils.CreateK8sContainerID(notif.Container.K8s.Namespace, notif.Container.K8s.PodName, notif.Container.K8s.ContainerName)
	ctx, span := otel.Tracer("").Start(am.ctx, "NetworkManager.ContainerCallback", trace.WithAttributes(attribute.String("containerID", notif.Container.Runtime.ContainerID), attribute.String("k8s workload", k8sContainerID)))
	defer span.End()

	switch notif.Type {
	case containercollection.EventTypeAddContainer:
		if am.watchedContainerChannels.Has(notif.Container.Runtime.ContainerID) {
			logger.L().Debug("container already exist in memory", helpers.String("container ID", notif.Container.Runtime.ContainerID), helpers.String("k8s workload", k8sContainerID))
			return
		}
		am.handleContainerStarted(ctx, notif.Container, k8sContainerID)

	case containercollection.EventTypeRemoveContainer:
	}
}

func (am *NetworkManager) SaveNetworkEvent(containerName, podName string, networkEvent *NetworkEvent) {
	networkEvents := am.containerAndPodToEventsMap.Get(containerName + podName)
	networkEvents = append(networkEvents, networkEvent)

	am.containerAndPodToEventsMap.Set(containerName+podName, networkEvents)
}

func (am *NetworkManager) handleContainerStarted(ctx context.Context, container *containercollection.Container, k8sContainerID string) {
	watchedContainer := &utils.WatchedContainerData{
		ContainerID:                              container.Runtime.ContainerID,
		UpdateDataTicker:                         time.NewTicker(am.cfg.InitialDelay),
		SyncChannel:                              make(chan error, 10),
		K8sContainerID:                           k8sContainerID,
		RelevantRealtimeFilesByPackageSourceInfo: map[string]*utils.PackageSourceInfoData{},
		RelevantRealtimeFilesBySPDXIdentifier:    map[v1beta1.ElementID]bool{},
	}
	am.watchedContainerChannels.Set(watchedContainer.ContainerID, watchedContainer.SyncChannel)

	// retrieve parent WL
	parentWL, err := am.getParentWorkloadFromContainer(container)
	if err != nil {
		logger.L().Info("NetworkManager - failed to get parent workload", helpers.String("reason", err.Error()), helpers.String("container ID", container.Runtime.ContainerID), helpers.String("k8s workload", k8sContainerID))
		return
	}

	selector, err := parentWL.GetSelector()
	if err != nil {
		logger.L().Info("NetworkManager - failed to get selector", helpers.String("reason", err.Error()), helpers.String("container ID", container.Runtime.ContainerID), helpers.String("k8s workload", k8sContainerID))
		return
	}

	// TODO: check if it has network neighbor on storage
	// If yes, update labels
	am.patchNetworkNeighbor(nil)

	// If not, create CRD
	networkNeighbors := createNetworkNeighborsCRD(parentWL, selector)
	am.publishNetworkNeighbors(networkNeighbors)

	// save container + pod to wlid map
	am.containerAndPodToWLIDMap.Set(container.Runtime.ContainerID+container.K8s.PodName, parentWL.GenerateWlid(am.clusterName))

	if err := am.monitorContainer(ctx, container, watchedContainer); err != nil {
		logger.L().Info("NetworkManager - stop monitor on container", helpers.String("reason", err.Error()), helpers.String("container ID", container.Runtime.ContainerID), helpers.String("k8s workload", k8sContainerID))
	}

	am.handleContainerStopped(container)
}

// TODO: implement
func (am *NetworkManager) patchNetworkNeighbor(networkNeighbor *NetworkNeighbors) {
	// patch to storage
}

// TODO: implement
func (am *NetworkManager) publishNetworkNeighbors(networkNeighbor *NetworkNeighbors) {
	// publish to storage
}

// TODO: use same function in relevancy
func (am *NetworkManager) getParentWorkloadFromContainer(container *containercollection.Container) (k8sinterface.IWorkload, error) {
	wl, err := am.k8sClient.GetWorkload(container.K8s.Namespace, "Pod", container.K8s.PodName)
	if err != nil {
		return nil, err
	}
	pod := wl.(*workloadinterface.Workload)

	// find parentWlid
	kind, name, err := am.k8sClient.CalculateWorkloadParentRecursive(pod)
	if err != nil {
		return nil, err
	}

	parentWorkload, err := am.k8sClient.GetWorkload(pod.GetNamespace(), kind, name)
	if err != nil {
		return nil, err
	}

	w := parentWorkload.(*workloadinterface.Workload)
	parentWlid := w.GenerateWlid(am.clusterName)

	err = wlid.IsWlidValid(parentWlid)
	if err != nil {
		return nil, err
	}

	return parentWorkload, nil
}

func (am *NetworkManager) handleContainerStopped(container *containercollection.Container) {
	// clean up
	am.containerAndPodToWLIDMap.Delete(container.Runtime.ContainerID + container.K8s.PodName)
	am.containerAndPodToEventsMap.Delete(container.Runtime.ContainerID + container.K8s.PodName)
	am.watchedContainerChannels.Delete(container.Runtime.ContainerID)
}

// TODO: implement
func (am *NetworkManager) monitorContainer(ctx context.Context, container *containercollection.Container, watchedContainer *utils.WatchedContainerData) error {
	for {
		select {
		case <-watchedContainer.UpdateDataTicker.C:
			// adjust ticker after first tick
			if !watchedContainer.InitialDelayExpired {
				watchedContainer.InitialDelayExpired = true
				watchedContainer.UpdateDataTicker.Reset(am.cfg.UpdateDataPeriod)
			}

			am.handleNetworkEvents(ctx, container, watchedContainer)
		case err := <-watchedContainer.SyncChannel:
			switch {
			case errors.Is(err, utils.ContainerHasTerminatedError):
				am.handleNetworkEvents(ctx, container, watchedContainer)
				return nil
			}
		}
	}
}

func (am *NetworkManager) handleNetworkEvents(ctx context.Context, container *containercollection.Container, watchedContainer *utils.WatchedContainerData) {
	fmt.Print(am.containerAndPodToEventsMap.Get(container.Runtime.ContainerID + container.K8s.PodName))

	// TODO: dns enrichment

	// update CRD based on events
	parentWlid := am.containerAndPodToWLIDMap.Get(container.Runtime.ContainerID + container.K8s.PodName)

	namespace := wlid.GetNamespaceFromWlid(parentWlid)
	kind := wlid.GetKindFromWlid(parentWlid)
	name := wlid.GetNameFromWlid(parentWlid)
	fmt.Printf("namespace: %s, kind: %s, name: %s\n", namespace, kind, name)

	//TODO: issue patch command
}
