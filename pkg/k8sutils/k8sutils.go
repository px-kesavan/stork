package k8sutils

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/portworx/sched-ops/k8s/apiextensions"
	"github.com/portworx/sched-ops/k8s/apps"
	"github.com/portworx/sched-ops/k8s/core"
	v1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	crdTimeout    = 1 * time.Minute
	retryInterval = 5 * time.Second
	// StorkDeploymentName - stork deployment name
	StorkDeploymentName = "stork"
	storkPodLabelKey    = "name"
	storkPodLabelValue  = "stork"
	// DefaultAdminNamespace - default admin namespace, where stork will be installed
	DefaultAdminNamespace = "kube-system"
	// StorkConfigMapName holds any generic config specific to stork module
	StorkConfigMapName = "stork-objlock-config"
	// ObjectLockIncrBackupCountKey defines scheduleBackup's incremental backup count
	ObjectLockIncrBackupCountKey = "object-lock-incr-backup-count"
	// ObjectLockDefaultIncrementalCount defines default incremental backup count
	ObjectLockDefaultIncrementalCount = 5
	//minProtectionPeriod defines minimum number of days, the backup are protected via object-lock feature
	minProtectionPeriod = 1
)

// GetPVCsForGroupSnapshot returns all PVCs in given namespace that match the given matchLabels. All PVCs need to be bound.
func GetPVCsForGroupSnapshot(namespace string, matchLabels map[string]string) ([]v1.PersistentVolumeClaim, error) {
	pvcList, err := core.Instance().GetPersistentVolumeClaims(namespace, matchLabels)
	if err != nil {
		return nil, err
	}

	if len(pvcList.Items) == 0 {
		return nil, fmt.Errorf("found no PVCs for group snapshot with given label selectors: %v", matchLabels)
	}

	// Check if no PVCs are in pending state
	for _, pvc := range pvcList.Items {
		if pvc.Status.Phase == v1.ClaimPending {
			return nil, fmt.Errorf("PVC: [%s] %s is still in %s phase. Group snapshot will trigger after all PVCs are bound",
				pvc.Namespace, pvc.Name, pvc.Status.Phase)
		}
	}

	return pvcList.Items, nil
}

// GetVolumeNamesFromLabelSelector returns PV names for all PVCs in given namespace that match the given
// labels
func GetVolumeNamesFromLabelSelector(namespace string, labels map[string]string) ([]string, error) {
	pvcs, err := GetPVCsForGroupSnapshot(namespace, labels)
	if err != nil {
		return nil, err
	}

	volNames := make([]string, 0)
	for _, pvc := range pvcs {
		volName, err := core.Instance().GetVolumeForPersistentVolumeClaim(&pvc)
		if err != nil {
			return nil, err
		}

		volNames = append(volNames, volName)
	}

	return volNames, nil
}

// ValidateCRD validate crd with apiversion v1beta1
func ValidateCRD(client *clientset.Clientset, crdName string) error {
	return wait.PollImmediate(retryInterval, crdTimeout, func() (bool, error) {
		crd, err := client.ApiextensionsV1beta1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		for _, cond := range crd.Status.Conditions {
			switch cond.Type {
			case apiextensionsv1beta1.Established:
				if cond.Status == apiextensionsv1beta1.ConditionTrue {
					return true, nil
				}
			case apiextensionsv1beta1.NamesAccepted:
				if cond.Status == apiextensionsv1beta1.ConditionFalse {
					return false, fmt.Errorf("name conflict: %v", cond.Reason)
				}
			}
		}
		return false, nil
	})
}

// ValidateCRDV1 validate crd with apiversion v1
func ValidateCRDV1(client *clientset.Clientset, crdName string) error {
	return wait.PollImmediate(retryInterval, crdTimeout, func() (bool, error) {
		crd, err := client.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		for _, cond := range crd.Status.Conditions {
			switch cond.Type {
			case apiextensionsv1.Established:
				if cond.Status == apiextensionsv1.ConditionTrue {
					return true, nil
				}
			case apiextensionsv1.NamesAccepted:
				if cond.Status == apiextensionsv1.ConditionFalse {
					return false, fmt.Errorf("name conflict: %v", cond.Reason)
				}
			}
		}
		return false, nil
	})
}

// CreateCRD creates the given custom resource
func CreateCRD(resource apiextensions.CustomResource) error {
	scope := apiextensionsv1.NamespaceScoped
	if string(resource.Scope) == string(apiextensionsv1.ClusterScoped) {
		scope = apiextensionsv1.ClusterScoped
	}
	ignoreSchemaValidation := true
	crdName := fmt.Sprintf("%s.%s", resource.Plural, resource.Group)
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: resource.Group,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: resource.Version,
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							XPreserveUnknownFields: &ignoreSchemaValidation,
						},
					},
				},
			},
			Scope: scope,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Singular:   resource.Name,
				Plural:     resource.Plural,
				Kind:       resource.Kind,
				ShortNames: resource.ShortNames,
			},
		},
	}
	err := apiextensions.Instance().RegisterCRD(crd)
	if err != nil {
		return err
	}
	return nil
}

// GetImageRegistryFromDeployment - extract image registry and image registry secret from deployment spec
func GetImageRegistryFromDeployment(name, namespace string) (string, string, error) {
	deploy, err := apps.Instance().GetDeployment(name, namespace)
	if err != nil {
		return "", "", err
	}
	imageFields := strings.Split(deploy.Spec.Template.Spec.Containers[0].Image, "/")
	var registry string
	// Here the assumtption is that the image format will be <registry-name>/<repo-name>/image:tag
	// or <repo-name>/image:tag. If repo name contains any path (<registry-name>/<repo-name>/<extra-dir-name>/image:tag), below logic will not work.
	if len(imageFields) == 3 {
		registry = imageFields[0]
	} else {
		registry = ""
	}
	imageSecret := deploy.Spec.Template.Spec.ImagePullSecrets
	if imageSecret != nil {
		return registry, imageSecret[0].Name, nil
	}
	return registry, "", nil
}

// GetStorkPodNamespace - will return the stork pod namespace.
func GetStorkPodNamespace() (string, error) {
	var ns string
	pods, err := core.Instance().ListPods(
		map[string]string{
			storkPodLabelKey: storkPodLabelValue,
		},
	)
	if err != nil {
		return ns, err
	}
	if len(pods.Items) > 0 {
		ns = pods.Items[0].Namespace
	}
	if len(ns) == 0 {
		return ns, fmt.Errorf("error: stork namespace is empty")
	}
	return ns, nil

}

// GetConfigValue read configmap and return the value of the requested parameter
func GetConfigValue(cm, ns, key string) (string, error) {
	configMap, err := core.Instance().GetConfigMap(
		cm,
		ns,
	)
	if err != nil {
		return "", fmt.Errorf("failed to get value for key [%v] from configmap[%v]", key, cm)
	}
	return configMap.Data[key], nil
}

// IsValidBucketRetentionPeriod - returns the sanity of retention period
// for a object locked bucket this function returns true if retention
// period set on the bucket is more than the minimum retention period
func IsValidBucketRetentionPeriod(bucketRetentionPeriod int64) (bool, error) {
	var incrBkpCnt int64
	var i string
	var err error
	ns := DefaultAdminNamespace
	if i, err = GetConfigValue(StorkConfigMapName, ns, ObjectLockIncrBackupCountKey); err != nil {
		return false, fmt.Errorf("failed to get %s key from px-backup-configmap: %v", ObjectLockIncrBackupCountKey, err)
	}
	if i != "" {
		incrBkpCnt, err = strconv.ParseInt(i, 10, 64)
		if err != nil {
			return false, fmt.Errorf("failed to convert backup incremental count: %v", err)
		}
	} else {
		incrBkpCnt = ObjectLockDefaultIncrementalCount
	}
	// Considering at least a day of protection, no. incremental backup each day
	// and a full backup following it makes up the minimum number of retention period
	// user should set.
	minRetentionDays := minProtectionPeriod + incrBkpCnt + 1
	return (bucketRetentionPeriod >= minRetentionDays), nil
}
