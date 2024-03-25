package applicationprofilecache

import (
	"context"
	"encoding/json"
	"node-agent/pkg/k8sclient"
	"node-agent/pkg/ruleengine/objectcache"
	"node-agent/pkg/watcher"

	mapset "github.com/deckarep/golang-set/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/goradd/maps"
	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1"
	"github.com/kubescape/k8s-interface/names"
	"github.com/kubescape/k8s-interface/workloadinterface"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ objectcache.ApplicationProfileCache = (*ApplicationProfileCacheImpl)(nil)
var _ watcher.Adaptor = (*ApplicationProfileCacheImpl)(nil)

type ApplicationProfileCacheImpl struct {
	nodeName         string
	k8sClient        k8sclient.K8sClientInterface
	podToSlug        maps.SafeMap[string, string]                      // cache the pod to slug mapping, this will enable a quick lookup of the application profile
	slugToAppProfile maps.SafeMap[string, *v1beta1.ApplicationProfile] // cache the application profile
	slugToPods       maps.SafeMap[string, mapset.Set[string]]          // cache the pods that belong to the application profile, this will enable removing from cache AP without pods
	allProfiles      mapset.Set[string]                                // cache all the application profiles that are ready. this will enable removing from cache AP without pods that are running on the same node
}

func NewApplicationProfileCache(nodeName string, k8sClient k8sclient.K8sClientInterface) *ApplicationProfileCacheImpl {
	return &ApplicationProfileCacheImpl{
		nodeName:    nodeName,
		k8sClient:   k8sClient,
		podToSlug:   maps.SafeMap[string, string]{},
		slugToPods:  maps.SafeMap[string, mapset.Set[string]]{},
		allProfiles: mapset.NewSet[string](),
	}

}

// ------------------ objectcache.ApplicationProfileCache methods -----------------------

func (ap *ApplicationProfileCacheImpl) GetApplicationProfile(namespace, name string) *v1beta1.ApplicationProfile {
	uniqueName := objectcache.UniqueName(namespace, name)
	if ap.slugToAppProfile.Has(uniqueName) {
		return ap.slugToAppProfile.Get(uniqueName)
	}
	return nil
}

// ------------------ watcher.Adaptor methods -----------------------

// ------------------ watcher.WatchResources methods -----------------------

func (ap *ApplicationProfileCacheImpl) WatchResources() []watcher.WatchResource {
	w := []watcher.WatchResource{}

	// add pod
	p := watcher.NewWatchResource(schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "pods",
	},
		metav1.ListOptions{
			FieldSelector: "spec.nodeName=" + ap.nodeName,
		},
	)
	w = append(w, p)

	// add application profile
	apl := watcher.NewWatchResource(schema.GroupVersionResource{
		Group:    "spdx.softwarecomposition.kubescape.io",
		Version:  "v1beta1",
		Resource: "applicationprofiles",
	}, metav1.ListOptions{})
	w = append(w, apl)

	return w
}

// ------------------ watcher.Watcher methods -----------------------
func (ap *ApplicationProfileCacheImpl) AddHandler(ctx context.Context, obj *unstructured.Unstructured) {
	switch obj.GetKind() {
	case "Pod":
		ap.addPod(obj)
	case "ApplicationProfile":
		ap.addApplicationProfile(ctx, obj)
	}
}
func (ap *ApplicationProfileCacheImpl) ModifyHandler(ctx context.Context, obj *unstructured.Unstructured) {
	switch obj.GetKind() {
	case "Pod":
		// do nothing
	case "ApplicationProfile":
		ap.addApplicationProfile(ctx, obj)
	}
}
func (ap *ApplicationProfileCacheImpl) DeleteHandler(_ context.Context, obj *unstructured.Unstructured) {
	switch obj.GetKind() {
	case "Pod":
		ap.deletePod(obj)
	case "ApplicationProfile":
		ap.deleteApplicationProfile(obj)
	}
}

// ------------------ watch pod methods -----------------------

func (ap *ApplicationProfileCacheImpl) addPod(podU *unstructured.Unstructured) {
	podName := objectcache.UnstructuredUniqueName(podU)

	if ap.podToSlug.Has(podName) {
		return
	}
	podB, err := podU.MarshalJSON()
	if err != nil {
		return
	}

	pod, err := workloadinterface.NewWorkload(podB)
	if err != nil {
		return
	}

	// get instanceIDs
	instanceIDs, err := instanceidhandler.GenerateInstanceID(pod)
	if err != nil {
		return
	}
	if len(instanceIDs) == 0 {
		return
	}

	// a single pod can have multiple instanceIDs (because of the containers), but we only need one
	instanceID := instanceIDs[0]
	slug, err := names.InstanceIDToSlug(instanceID.GetName(), instanceID.GetKind(), "", instanceID.GetHashed())
	if err != nil {
		return
	}
	uniqueSlug := objectcache.UniqueName(pod.GetNamespace(), slug)
	ap.podToSlug.Set(podName, uniqueSlug)

	if !ap.slugToPods.Has(uniqueSlug) {
		ap.slugToPods.Set(uniqueSlug, mapset.NewSet[string]())
	}
	ap.slugToPods.Get(uniqueSlug).Add(podName)

	// if application profile exists but is not cached
	if ap.allProfiles.Contains(uniqueSlug) && !ap.slugToAppProfile.Has(uniqueSlug) {

		// get the application profile
		appProfile, err := ap.getApplicationProfile(pod.GetNamespace(), slug)
		if err != nil {
			logger.L().Error("failed to get application profile", helpers.Error(err))
			return
		}
		ap.slugToAppProfile.Set(uniqueSlug, appProfile)
	}
}

func (ap *ApplicationProfileCacheImpl) deletePod(obj *unstructured.Unstructured) {
	podName := objectcache.UnstructuredUniqueName(obj)
	uniqueSlug := ap.podToSlug.Get(podName)
	ap.podToSlug.Delete(podName)

	// remove pod form the application profile mapping
	if ap.slugToPods.Has(uniqueSlug) {
		ap.slugToPods.Get(uniqueSlug).Remove(podName)
		if ap.slugToPods.Get(uniqueSlug).Cardinality() == 0 {
			ap.slugToPods.Delete(uniqueSlug)
			// remove full application profile from cache
			ap.slugToAppProfile.Delete(uniqueSlug)
		}
	}
}

// ------------------ watch application profile methods -----------------------
func (ap *ApplicationProfileCacheImpl) addApplicationProfile(_ context.Context, obj *unstructured.Unstructured) {
	apName := objectcache.UnstructuredUniqueName(obj)

	appProfile, err := unstructuredToApplicationProfile(obj)
	if err != nil {
		logger.L().Error("failed to unmarshal application profile", helpers.Error(err))
		return
	}

	// check if the application profile is ready
	// TODO: @amir
	// if was ready and now is not, remove from cache
	// if ap.slugToAppProfile.Has(apName) {
	// 	return
	// }

	// get the full application profile from the storage
	// the watch only returns the metadata
	fullAP, err := ap.getApplicationProfile(appProfile.GetNamespace(), appProfile.GetName())
	if err != nil {
		logger.L().Error("failed to get full application profile", helpers.Error(err))
		return
	}

	ap.slugToAppProfile.Set(apName, fullAP)
	ap.allProfiles.Add(apName)
	ap.podToSlug.Range(func(podName, uniqueSlug string) bool {
		if uniqueSlug == apName {
			if !ap.slugToPods.Has(uniqueSlug) {
				ap.slugToPods.Set(uniqueSlug, mapset.NewSet[string]())
			}
			ap.slugToPods.Get(uniqueSlug).Add(podName)
		}
		return true
	})
}

func (ap *ApplicationProfileCacheImpl) deleteApplicationProfile(obj *unstructured.Unstructured) {
	apName := objectcache.UnstructuredUniqueName(obj)
	ap.slugToAppProfile.Delete(apName)
	ap.allProfiles.Remove(apName)
	ap.slugToPods.Delete(apName)
}

func unstructuredToApplicationProfile(obj *unstructured.Unstructured) (*v1beta1.ApplicationProfile, error) {
	bytes, err := obj.MarshalJSON()
	if err != nil {
		return nil, err
	}

	var ap *v1beta1.ApplicationProfile
	err = json.Unmarshal(bytes, ap)
	if err != nil {
		return nil, err
	}
	return ap, nil
}
func (ap *ApplicationProfileCacheImpl) getApplicationProfile(namespace, name string) (*v1beta1.ApplicationProfile, error) {
	gvr := schema.GroupVersionResource{
		Group:    "spdx.softwarecomposition.kubescape.io",
		Version:  "v1beta1",
		Resource: "applicationprofiles",
	}
	u, err := ap.k8sClient.GetDynamicClient().Resource(gvr).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return unstructuredToApplicationProfile(u)
}
