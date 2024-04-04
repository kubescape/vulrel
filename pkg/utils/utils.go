package utils

import (
	"errors"
	"fmt"
	"math/rand"
	"node-agent/pkg/objectcache"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"

	"github.com/goradd/maps"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/containerinstance"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/ephemeralcontainerinstance"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/initcontainerinstance"
	"github.com/kubescape/k8s-interface/workloadinterface"

	"github.com/armosec/utils-k8s-go/wlid"
	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/k8s-interface/instanceidhandler"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"k8s.io/apimachinery/pkg/util/validation"
)

var (
	ContainerHasTerminatedError     = errors.New("container has terminated")
	TooLargeApplicationProfileError = errors.New("application profile is too large")
	IncompleteSBOMError             = errors.New("incomplete SBOM")
)

type PackageSourceInfoData struct {
	Exist                 bool
	PackageSPDXIdentifier []v1beta1.ElementID
}

type ContainerType int

const (
	Unknown = iota
	Container
	InitContainer
	EphemeralContainer
)

type WatchedContainerStatus string

const (
	WatchedContainerStatusInitializing WatchedContainerStatus = helpersv1.Initializing
	WatchedContainerStatusReady        WatchedContainerStatus = helpersv1.Ready
	WatchedContainerStatusCompleted    WatchedContainerStatus = helpersv1.Completed

	WatchedContainerStatusMissingRuntime WatchedContainerStatus = helpersv1.MissingRuntime
	WatchedContainerStatusTooLarge       WatchedContainerStatus = helpersv1.TooLarge
)

type WatchedContainerCompletionStatus string

const (
	WatchedContainerCompletionStatusPartial WatchedContainerCompletionStatus = helpersv1.Partial
	WatchedContainerCompletionStatusFull    WatchedContainerCompletionStatus = helpersv1.Complete
)

func (c ContainerType) String() string {
	return [...]string{"unknown", "containers", "initContainers", "ephemeralContainers"}[c]
}

type WatchedContainerData struct {
	InstanceID                                 instanceidhandler.IInstanceID
	UpdateDataTicker                           *time.Ticker
	SyncChannel                                chan error
	SBOMSyftFiltered                           *v1beta1.SBOMSyftFiltered
	RelevantRealtimeFilesByIdentifier          map[string]bool
	RelevantRelationshipsArtifactsByIdentifier map[string]bool
	RelevantArtifactsFilesByIdentifier         map[string]bool
	ParentResourceVersion                      string
	ContainerID                                string
	ImageTag                                   string
	ImageID                                    string
	Wlid                                       string
	TemplateHash                               string
	K8sContainerID                             string
	SBOMResourceVersion                        int
	ContainerType                              ContainerType
	ContainerIndex                             int
	NsMntId                                    uint64
	InitialDelayExpired                        bool

	statusUpdated    bool
	status           WatchedContainerStatus
	completionStatus WatchedContainerCompletionStatus
}

func Between(value string, a string, b string) string {
	// Get substring between two strings.
	posFirst := strings.Index(value, a)
	if posFirst == -1 {
		return ""
	}
	substr := value[posFirst+len(a):]
	posLast := strings.Index(substr, b) + posFirst + len(a)
	if posLast == -1 {
		return ""
	}
	posFirstAdjusted := posFirst + len(a)
	if posFirstAdjusted >= posLast {
		return ""
	}
	return value[posFirstAdjusted:posLast]
}

func After(value string, a string) string {
	// Get substring after a string.
	pos := strings.LastIndex(value, a)
	if pos == -1 {
		return ""
	}
	adjustedPos := pos + len(a)
	if adjustedPos >= len(value) {
		return ""
	}
	return value[adjustedPos:]
}

func CurrentDir() string {
	_, filename, _, _ := runtime.Caller(1)

	return filepath.Dir(filename)
}

func CreateK8sContainerID(namespaceName string, podName string, containerName string) string {
	return strings.Join([]string{namespaceName, podName, containerName}, "/")
}

// AddRandomDuration adds between min and max seconds to duration
func AddRandomDuration(min, max int, duration time.Duration) time.Duration {
	// we don't initialize the seed, so we will get the same sequence of random numbers every time
	randomDuration := time.Duration(rand.Intn(max+1-min)+min) * time.Second
	return randomDuration + duration
}

func Atoi(s string) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return i
}

func GetLabels(watchedContainer *WatchedContainerData, stripContainer bool) map[string]string {
	labels := watchedContainer.InstanceID.GetLabels()
	for i := range labels {
		if labels[i] == "" {
			delete(labels, i)
		} else if stripContainer && i == helpersv1.ContainerNameMetadataKey {
			delete(labels, i)
		} else {
			if i == helpersv1.KindMetadataKey {
				labels[i] = wlid.GetKindFromWlid(watchedContainer.Wlid)
			} else if i == helpersv1.NameMetadataKey {
				labels[i] = wlid.GetNameFromWlid(watchedContainer.Wlid)
			}
			errs := validation.IsValidLabelValue(labels[i])
			if len(errs) != 0 {
				logger.L().Debug("label is not valid", helpers.String("label", labels[i]))
				for j := range errs {
					logger.L().Debug("label err description", helpers.String("Err: ", errs[j]))
				}
				delete(labels, i)
			}
		}
	}
	if watchedContainer.ParentResourceVersion != "" {
		labels[helpersv1.ResourceVersionMetadataKey] = watchedContainer.ParentResourceVersion
	}
	if watchedContainer.TemplateHash != "" {
		labels[helpersv1.TemplateHashKey] = watchedContainer.TemplateHash
	}

	return labels
}

func GetApplicationProfileContainer(profile *v1beta1.ApplicationProfile, containerType ContainerType, containerIndex int) *v1beta1.ApplicationProfileContainer {
	if profile == nil {
		return nil
	}
	switch containerType {
	case Container:
		if len(profile.Spec.Containers) > containerIndex {
			return &profile.Spec.Containers[containerIndex]
		}
	case InitContainer:
		if len(profile.Spec.InitContainers) > containerIndex {
			return &profile.Spec.InitContainers[containerIndex]
		}
	case EphemeralContainer:
		if len(profile.Spec.EphemeralContainers) > containerIndex {
			return &profile.Spec.EphemeralContainers[containerIndex]
		}
	}
	return nil
}

func InsertApplicationProfileContainer(profile *v1beta1.ApplicationProfile, containerType ContainerType, containerIndex int, profileContainer *v1beta1.ApplicationProfileContainer) {
	switch containerType {
	case Container:
		if len(profile.Spec.Containers) <= containerIndex {
			profile.Spec.Containers = append(profile.Spec.Containers, make([]v1beta1.ApplicationProfileContainer, containerIndex-len(profile.Spec.Containers)+1)...)
		}
		profile.Spec.Containers[containerIndex] = *profileContainer
	case InitContainer:
		if len(profile.Spec.InitContainers) <= containerIndex {
			profile.Spec.InitContainers = append(profile.Spec.InitContainers, make([]v1beta1.ApplicationProfileContainer, containerIndex-len(profile.Spec.InitContainers)+1)...)
		}
		profile.Spec.InitContainers[containerIndex] = *profileContainer
	case EphemeralContainer:
		if len(profile.Spec.EphemeralContainers) <= containerIndex {
			profile.Spec.EphemeralContainers = append(profile.Spec.EphemeralContainers, make([]v1beta1.ApplicationProfileContainer, containerIndex-len(profile.Spec.EphemeralContainers)+1)...)
		}
		profile.Spec.EphemeralContainers[containerIndex] = *profileContainer
	}
}

func (watchedContainer *WatchedContainerData) GetStatus() WatchedContainerStatus {
	return watchedContainer.status
}

func (watchedContainer *WatchedContainerData) GetCompletionStatus() WatchedContainerCompletionStatus {
	return watchedContainer.completionStatus
}

func (watchedContainer *WatchedContainerData) SetStatus(newStatus WatchedContainerStatus) {
	if newStatus != watchedContainer.status {
		watchedContainer.status = newStatus
		watchedContainer.statusUpdated = true
	}
}

func (watchedContainer *WatchedContainerData) SetCompletionStatus(newStatus WatchedContainerCompletionStatus) {
	if newStatus != watchedContainer.completionStatus {
		watchedContainer.completionStatus = newStatus
		watchedContainer.statusUpdated = true
	}
}

func (watchedContainer *WatchedContainerData) ResetStatusUpdatedFlag() {
	watchedContainer.statusUpdated = false
}

func (watchedContainer *WatchedContainerData) StatusUpdated() bool {
	return watchedContainer.statusUpdated
}

func (watchedContainer *WatchedContainerData) SetContainerType(wl workloadinterface.IWorkload, containerName string) {
	containers, err := wl.GetContainers()
	if err != nil {
		return
	}
	for i, c := range containers {
		if c.Name == containerName {
			watchedContainer.ContainerIndex = i
			watchedContainer.ContainerType = Container
			break
		}
	}
	// initContainers
	initContainers, err := wl.GetInitContainers()
	if err != nil {
		return
	}
	for i, c := range initContainers {
		if c.Name == containerName {
			watchedContainer.ContainerIndex = i
			watchedContainer.ContainerType = InitContainer
			break
		}
	}
	// ephemeralContainers
	ephemeralContainers, err := wl.GetEphemeralContainers()
	if err != nil {
		return
	}
	for i, c := range ephemeralContainers {
		if c.Name == containerName {
			watchedContainer.ContainerIndex = i
			watchedContainer.ContainerType = EphemeralContainer
			break
		}
	}
}

// SetTerminationStatus updates the terminated flag and sets the exit code on the watched container
func (watchedContainer *WatchedContainerData) GetTerminationExitCode(k8sObjectsCache objectcache.K8sObjectCache, namespace, podName, containerName string) int32 {
	time.Sleep(3 * time.Second)
	podStatus := k8sObjectsCache.GetPodStatus(namespace, podName)
	if podStatus != nil {
		for i := range podStatus.ContainerStatuses {
			if podStatus.ContainerStatuses[i].Name == containerName {
				if podStatus.ContainerStatuses[i].LastTerminationState.Terminated != nil {
					return podStatus.ContainerStatuses[i].LastTerminationState.Terminated.ExitCode

				}
			}
		}

		// in case the terminated container is an init or ephemeral container
		// return -1 to avoid setting the status later to completed
		for i := range podStatus.InitContainerStatuses {
			if podStatus.InitContainerStatuses[i].Name == containerName {
				return -1
			}
		}

		for i := range podStatus.EphemeralContainerStatuses {
			if podStatus.EphemeralContainerStatuses[i].Name == containerName {
				return -1
			}
		}
	}

	return 0
}

func EnrichProfileContainer(newProfileContainer *v1beta1.ApplicationProfileContainer, observedCapabilities, observedSyscalls []string, execs map[string]mapset.Set[string], opens map[string]mapset.Set[string]) {
	// add capabilities
	sort.Strings(observedCapabilities)
	newProfileContainer.Capabilities = observedCapabilities
	// add syscalls
	sort.Strings(observedSyscalls)
	newProfileContainer.Syscalls = observedSyscalls
	// add execs
	newProfileContainer.Execs = make([]v1beta1.ExecCalls, 0)
	for path, exec := range execs {
		args := exec.ToSlice()
		sort.Strings(args)
		newProfileContainer.Execs = append(newProfileContainer.Execs, v1beta1.ExecCalls{
			Path: path,
			Args: args,
		})
	}
	// add opens
	newProfileContainer.Opens = make([]v1beta1.OpenCalls, 0)
	for path, open := range opens {
		flags := open.ToSlice()
		sort.Strings(flags)
		newProfileContainer.Opens = append(newProfileContainer.Opens, v1beta1.OpenCalls{
			Path:  path,
			Flags: flags,
		})
	}
}

type PatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

// EscapeJSONPointerElement escapes a JSON pointer element
// See https://www.rfc-editor.org/rfc/rfc6901#section-3
func EscapeJSONPointerElement(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

func CreateCapabilitiesPatchOperations(capabilities, syscalls []string, execs map[string]mapset.Set[string], opens map[string]mapset.Set[string], containerType string, containerIndex int) []PatchOperation {
	var profileOperations []PatchOperation
	// add capabilities
	sort.Strings(capabilities)
	capabilitiesPath := fmt.Sprintf("/spec/%s/%d/capabilities/-", containerType, containerIndex)
	for _, capability := range capabilities {
		profileOperations = append(profileOperations, PatchOperation{
			Op:    "add",
			Path:  capabilitiesPath,
			Value: capability,
		})
	}
	// add syscalls
	sort.Strings(syscalls)
	sysCallsPath := fmt.Sprintf("/spec/%s/%d/syscalls/-", containerType, containerIndex)
	for _, syscall := range syscalls {
		profileOperations = append(profileOperations, PatchOperation{
			Op:    "add",
			Path:  sysCallsPath,
			Value: syscall,
		})
	}

	// add execs
	execsPath := fmt.Sprintf("/spec/%s/%d/execs/-", containerType, containerIndex)
	for path, exec := range execs {
		args := exec.ToSlice()
		sort.Strings(args)
		profileOperations = append(profileOperations, PatchOperation{
			Op:   "add",
			Path: execsPath,
			Value: v1beta1.ExecCalls{
				Path: path,
				Args: args,
			},
		})
	}
	// add opens
	opensPath := fmt.Sprintf("/spec/%s/%d/opens/-", containerType, containerIndex)
	for path, open := range opens {
		flags := open.ToSlice()
		sort.Strings(flags)

		profileOperations = append(profileOperations, PatchOperation{
			Op:   "add",
			Path: opensPath,
			Value: v1beta1.OpenCalls{
				Path:  path,
				Flags: flags,
			},
		})
	}
	return profileOperations
}

func AppendStatusAnnotationPatchOperations(existingPatch []PatchOperation, watchedContainer *WatchedContainerData) []PatchOperation {
	if watchedContainer.statusUpdated {
		existingPatch = append(existingPatch, PatchOperation{
			Op:    "replace",
			Path:  "/metadata/annotations/" + EscapeJSONPointerElement(helpersv1.StatusMetadataKey),
			Value: string(watchedContainer.status),
		},
			PatchOperation{
				Op:    "replace",
				Path:  "/metadata/annotations/" + EscapeJSONPointerElement(helpersv1.CompletionMetadataKey),
				Value: string(watchedContainer.completionStatus),
			},
		)
	}

	return existingPatch
}

func SetInMap(newExecMap *maps.SafeMap[string, mapset.Set[string]]) func(k string, v mapset.Set[string]) bool {
	return func(k string, v mapset.Set[string]) bool {
		if newExecMap.Has(k) {
			newExecMap.Get(k).Append(v.ToSlice()...)
		} else {
			newExecMap.Set(k, v)
		}
		return true
	}
}

func ToInstanceType(c ContainerType) helpersv1.InstanceType {
	switch c {
	case Container:
		return containerinstance.InstanceType
	case InitContainer:
		return initcontainerinstance.InstanceType
	case EphemeralContainer:
		return ephemeralcontainerinstance.InstanceType
	}

	return containerinstance.InstanceType
}
