package util

import (
	"fmt"
	"path"
	"reflect"
	"strconv"
	"strings"

	"github.com/hashicorp/go-version"
	v1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/api/equality"

	corev1 "github.com/libopenstorage/operator/pkg/apis/core/v1"
	"github.com/libopenstorage/operator/pkg/constants"

	appsv1 "k8s.io/api/apps/v1"
)

// Reasons for controller events
const (
	// FailedPlacementReason is added to an event when operator can't schedule a Pod to a specified node.
	FailedPlacementReason = "FailedPlacement"
	// FailedStoragePodReason is added to an event when the status of a Pod of a cluster is 'Failed'.
	FailedStoragePodReason = "FailedStoragePod"
	// FailedSyncReason is added to an event when the status of the cluster could not be synced.
	FailedSyncReason = "FailedSync"
	// FailedValidationReason is added to an event when operator validations fail.
	FailedValidationReason = "FailedValidation"
	// FailedComponentReason is added to an event when setting up or removing a component fails.
	FailedComponentReason = "FailedComponent"
	// UpdatePausedReason is added to an event when operator pauses update of the storage cluster.
	UpdatePausedReason = "UpdatePaused"
	// ClusterOnlineReason is added to an event when a cluster comes online
	ClusterOnlineReason = "ClusterOnline"
	// MigrationPendingReason is added to an event when the migration is in pending state.
	MigrationPendingReason = "MigrationPending"
)

var (
	// commonDockerRegistries is a map of commonly used Docker registries
	commonDockerRegistries = map[string]bool{
		"docker.io":                   true,
		"quay.io":                     true,
		"index.docker.io":             true,
		"registry-1.docker.io":        true,
		"registry.connect.redhat.com": true,
	}
)

func getMergedCommonRegistries(cluster *corev1.StorageCluster) map[string]bool {
	val, ok := cluster.Annotations[constants.AnnotationCommonImageRegistries]

	if !ok {
		return commonDockerRegistries
	}

	mergedCommonRegistries := make(map[string]bool)

	for _, v := range strings.Split(strings.TrimSpace(val), ",") {
		mergedCommonRegistries[v] = true
	}

	for k, v := range commonDockerRegistries {
		mergedCommonRegistries[k] = v
	}

	return mergedCommonRegistries
}

// GetImageURN returns the complete image name based on the registry and repo
func GetImageURN(cluster *corev1.StorageCluster, image string) string {
	if image == "" {
		return ""
	}

	registryAndRepo := cluster.Spec.CustomImageRegistry
	mergedCommonRegistries := getMergedCommonRegistries(cluster)

	omitRepo := false
	if strings.HasSuffix(registryAndRepo, "//") {
		omitRepo = true
	}

	registryAndRepo = strings.TrimRight(registryAndRepo, "/")
	if registryAndRepo == "" {
		// no registry/repository specifed, return image
		return image
	}

	imgParts := strings.Split(image, "/")
	if len(imgParts) > 1 {
		// advance imgParts to swallow the common registry
		if _, present := mergedCommonRegistries[imgParts[0]]; present {
			imgParts = imgParts[1:]
		}
	}

	// if we have '/' in the registryAndRepo, return <registry/repository/><only-image>
	// else (registry only) -- return <registry/><image-with-repository>
	if strings.Contains(registryAndRepo, "/") || omitRepo {
		// advance to the last element, skipping image's repository
		imgParts = imgParts[len(imgParts)-1:]
	}
	return registryAndRepo + "/" + path.Join(imgParts...)
}

// GetImageMajorVersion returns the major version for a given image.
// This allows you to make decisions based on the major version.
func GetImageMajorVersion(image string) int {
	if image == "" {
		return -1
	}

	var tag string
	parts := strings.Split(image, ":")
	if len(parts) < 2 {
		return -1
	}

	tag = parts[1]
	if tag == "" {
		return -1
	}

	ver, err := version.NewVersion(tag)
	if err != nil {
		return -1
	}

	return ver.Segments()[0]
}

// HasPullSecretChanged checks if the imagePullSecret in the cluster is the only one
// in the given list of pull secrets
func HasPullSecretChanged(
	cluster *corev1.StorageCluster,
	existingPullSecrets []v1.LocalObjectReference,
) bool {
	return len(existingPullSecrets) > 1 ||
		(len(existingPullSecrets) == 1 &&
			cluster.Spec.ImagePullSecret != nil && existingPullSecrets[0].Name != *cluster.Spec.ImagePullSecret) ||
		(len(existingPullSecrets) == 0 &&
			cluster.Spec.ImagePullSecret != nil && *cluster.Spec.ImagePullSecret != "")
}

// HaveTolerationsChanged checks if the tolerations in the cluster are same as the
// given list of tolerations
func HaveTolerationsChanged(
	cluster *corev1.StorageCluster,
	existingTolerations []v1.Toleration,
) bool {
	if cluster.Spec.Placement == nil {
		return len(existingTolerations) != 0
	}
	return !reflect.DeepEqual(cluster.Spec.Placement.Tolerations, existingTolerations)
}

// DeploymentDeepEqual compares if two deployments are same.
func DeploymentDeepEqual(d1 *appsv1.Deployment, d2 *appsv1.Deployment) (bool, error) {
	// DeepDerivative will return true if first argument is nil, hence check the length of volumes.
	// The reason we don't use deepEqual for volumes is k8s API server may add defaultMode to it.
	if !equality.Semantic.DeepDerivative(d1.Spec.Template.Spec.Containers, d2.Spec.Template.Spec.Containers) {
		return false, fmt.Errorf("containers not equal, first: %+v, second: %+v", d1.Spec.Template.Spec.Containers, d2.Spec.Template.Spec.Containers)
	}

	if !(len(d1.Spec.Template.Spec.Volumes) == len(d2.Spec.Template.Spec.Volumes) &&
		equality.Semantic.DeepDerivative(d1.Spec.Template.Spec.Volumes, d2.Spec.Template.Spec.Volumes)) {
		return false, fmt.Errorf("volumes not equal, first: %+v, second: %+v", d1.Spec.Template.Spec.Volumes, d2.Spec.Template.Spec.Volumes)
	}

	if !equality.Semantic.DeepEqual(d1.Spec.Template.Spec.ImagePullSecrets, d2.Spec.Template.Spec.ImagePullSecrets) {
		return false, fmt.Errorf("image pull secrets not equal, first: %+v, second: %+v", d1.Spec.Template.Spec.ImagePullSecrets, d2.Spec.Template.Spec.ImagePullSecrets)
	}

	if !equality.Semantic.DeepEqual(d1.Spec.Template.Spec.Affinity, d2.Spec.Template.Spec.Affinity) {
		return false, fmt.Errorf("affinity not equal, first: %+v, second: %+v", d1.Spec.Template.Spec.Affinity, d2.Spec.Template.Spec.Affinity)
	}

	if !equality.Semantic.DeepEqual(d1.Spec.Template.Spec.Tolerations, d2.Spec.Template.Spec.Tolerations) {
		return false, fmt.Errorf("tolerations not equal, first: %+v, second: %+v", d1.Spec.Template.Spec.Tolerations, d2.Spec.Template.Spec.Tolerations)
	}

	if !equality.Semantic.DeepEqual(d1.Spec.Template.Spec.ServiceAccountName, d2.Spec.Template.Spec.ServiceAccountName) {
		return false, fmt.Errorf("service account name not equal, first: %s, second: %s", d1.Spec.Template.Spec.ServiceAccountName, d2.Spec.Template.Spec.ServiceAccountName)
	}

	return true, nil
}

// HasNodeAffinityChanged checks if the nodeAffinity in the cluster is same as the
// node affinity in the given affinity
func HasNodeAffinityChanged(
	cluster *corev1.StorageCluster,
	existingAffinity *v1.Affinity,
) bool {
	if cluster.Spec.Placement == nil {
		return existingAffinity != nil && existingAffinity.NodeAffinity != nil
	} else if existingAffinity == nil {
		return cluster.Spec.Placement.NodeAffinity != nil
	}
	return !reflect.DeepEqual(cluster.Spec.Placement.NodeAffinity, existingAffinity.NodeAffinity)
}

// ExtractVolumesAndMounts returns a list of Kubernetes volumes and volume mounts from the
// given StorageCluster volume specs
func ExtractVolumesAndMounts(volumeSpecs []corev1.VolumeSpec) ([]v1.Volume, []v1.VolumeMount) {
	volumes := make([]v1.Volume, 0)
	volumeMounts := make([]v1.VolumeMount, 0)

	// Set volume defaults. Makes it easier to compare with
	// actual deployment volumes to see if they have changed.
	for i := range volumeSpecs {
		if volumeSpecs[i].ConfigMap != nil {
			defaultMode := v1.ConfigMapVolumeSourceDefaultMode
			volumeSpecs[i].ConfigMap.DefaultMode = &defaultMode
		} else if volumeSpecs[i].Secret != nil {
			defaultMode := v1.SecretVolumeSourceDefaultMode
			volumeSpecs[i].Secret.DefaultMode = &defaultMode
		} else if volumeSpecs[i].Projected != nil {
			defaultMode := v1.ProjectedVolumeSourceDefaultMode
			volumeSpecs[i].Projected.DefaultMode = &defaultMode
		} else if volumeSpecs[i].HostPath != nil {
			hostPathType := v1.HostPathUnset
			volumeSpecs[i].HostPath.Type = &hostPathType
		}
	}

	for _, volumeSpec := range volumeSpecs {
		volumes = append(volumes, v1.Volume{
			Name:         volumeSpec.Name,
			VolumeSource: volumeSpec.VolumeSource,
		})
		volumeMounts = append(volumeMounts, v1.VolumeMount{
			Name:             volumeSpec.Name,
			MountPath:        volumeSpec.MountPath,
			MountPropagation: volumeSpec.MountPropagation,
			ReadOnly:         volumeSpec.ReadOnly,
		})
	}

	return volumes, volumeMounts
}

// IsPartialSecretRef is a helper method that checks if a SecretRef is partially specified (i.e. only one of the needed cert name and key specified)
func IsPartialSecretRef(sref *corev1.SecretRef) bool {
	if sref == nil {
		return false
	}
	x := len(sref.SecretName) > 0
	y := len(sref.SecretKey) > 0
	// X xor Y -> (X || Y) && !(X && Y)
	return (x || y) && !(x && y)
}

// GetCustomAnnotations returns custom annotations for different StorageCluster components from spec
func GetCustomAnnotations(
	cluster *corev1.StorageCluster,
	k8sObjKind string,
	componentName string,
) map[string]string {
	if cluster.Spec.Metadata == nil || cluster.Spec.Metadata.Annotations == nil {
		return nil
	}
	// Use kind/component to locate the custom annotation, e.g. deployment/stork
	key := fmt.Sprintf("%s/%s", k8sObjKind, componentName)
	if annotations, ok := cluster.Spec.Metadata.Annotations[key]; ok && len(annotations) != 0 {
		return annotations
	}
	return nil
}

// ComponentsPausedForMigration returns true if the daemonset migration is going on and
// the components are waiting for storage pods to migrate first
func ComponentsPausedForMigration(cluster *corev1.StorageCluster) bool {
	_, migrating := cluster.Annotations[constants.AnnotationMigrationApproved]
	componentsPaused, err := strconv.ParseBool(cluster.Annotations[constants.AnnotationPauseComponentMigration])
	return migrating && err == nil && componentsPaused
}
