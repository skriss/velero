/*
Copyright 2019 the Velero contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package restore

import (
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	storagev1client "k8s.io/client-go/kubernetes/typed/storage/v1"

	"github.com/heptio/velero/pkg/plugin/framework"
	"github.com/heptio/velero/pkg/plugin/velero"
)

// ChangeStorageClassAction updates a PV or PVC's storage class name
// if a mapping is found in the plugin's config map.
type ChangeStorageClassAction struct {
	logger             logrus.FieldLogger
	configMapClient    corev1client.ConfigMapInterface
	storageClassClient storagev1client.StorageClassInterface
}

// NewChangeStorageClassAction is the constructor for ChangeStorageClassAction.
func NewChangeStorageClassAction(
	logger logrus.FieldLogger,
	configMapClient corev1client.ConfigMapInterface,
	storageClassClient storagev1client.StorageClassInterface,
) *ChangeStorageClassAction {
	return &ChangeStorageClassAction{
		logger:             logger,
		configMapClient:    configMapClient,
		storageClassClient: storageClassClient,
	}
}

// AppliesTo returns the resources that ChangeStorageClassAction should
// be run for.
func (a *ChangeStorageClassAction) AppliesTo() (velero.ResourceSelector, error) {
	return velero.ResourceSelector{
		IncludedResources: []string{"persistentvolumeclaims", "persistentvolumes"},
	}, nil
}

// Execute updates the item's spec.storageClassName if a mapping is found
// in the config map for the plugin.
func (a *ChangeStorageClassAction) Execute(input *velero.RestoreItemActionExecuteInput) (*velero.RestoreItemActionExecuteOutput, error) {
	a.logger.Info("Executing ChangeStorageClassAction")
	defer a.logger.Info("Done executing ChangeStorageClassAction")

	a.logger.Debug("Getting plugin config")
	config, err := getPluginConfig(framework.PluginKindRestoreItemAction, "velero.io/change-storageclass", a.configMapClient)
	if err != nil {
		return nil, err
	}

	if config == nil || len(config.Data) == 0 {
		a.logger.Debug("No storage class mappings found")
		return velero.NewRestoreItemActionExecuteOutput(input.Item), nil
	}

	obj, ok := input.Item.(*unstructured.Unstructured)
	if !ok {
		return nil, errors.Errorf("object was of unexpected type %T", input.Item)
	}

	log := a.logger.WithFields(map[string]interface{}{
		"kind":      obj.GetKind(),
		"namespace": obj.GetNamespace(),
		"name":      obj.GetName(),
	})

	// use the unstructured helpers here since this code is for both PVs and PVCs, and the
	// field names are the same for both types.
	storageClass, _, err := unstructured.NestedString(obj.UnstructuredContent(), "spec", "storageClassName")
	if err != nil {
		return nil, errors.Wrap(err, "error getting item's spec.storageClassName")
	}
	if storageClass == "" {
		log.Debug("Item has no storage class specified")
		return velero.NewRestoreItemActionExecuteOutput(input.Item), nil
	}

	newStorageClass, ok := config.Data[storageClass]
	if !ok {
		log.Debugf("No mapping found for storage class %s", storageClass)
		return velero.NewRestoreItemActionExecuteOutput(input.Item), nil
	}

	// validate that new storage class exists
	if _, err := a.storageClassClient.Get(newStorageClass, metav1.GetOptions{}); err != nil {
		return nil, errors.Wrapf(err, "error getting storage class %s from API", newStorageClass)
	}

	log.Infof("Updating item's storage class name to %s", newStorageClass)

	if err := unstructured.SetNestedField(obj.UnstructuredContent(), newStorageClass, "spec", "storageClassName"); err != nil {
		return nil, errors.Wrap(err, "unable to set item's spec.storageClassName")
	}

	return velero.NewRestoreItemActionExecuteOutput(obj), nil
}
