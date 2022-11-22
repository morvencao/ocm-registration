package addon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"

	addonv1alpha1 "open-cluster-management.io/api/addon/v1alpha1"
	addonfake "open-cluster-management.io/api/client/addon/clientset/versioned/fake"
	addoninformers "open-cluster-management.io/api/client/addon/informers/externalversions"
	clusterfake "open-cluster-management.io/api/client/cluster/clientset/versioned/fake"
	clusterinformers "open-cluster-management.io/api/client/cluster/informers/externalversions"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	testinghelpers "open-cluster-management.io/registration/pkg/helpers/testing"
)

func TestGetAddOnLabelValue(t *testing.T) {
	cases := []struct {
		name            string
		addOnConditions []metav1.Condition
		expectedValue   string
	}{
		{
			name:          "no condition",
			expectedValue: addOnStatusUnreachable,
		},
		{
			name: "status is true",
			addOnConditions: []metav1.Condition{
				{
					Type:   addonv1alpha1.ManagedClusterAddOnConditionAvailable,
					Status: metav1.ConditionTrue,
				},
			},
			expectedValue: addOnStatusAvailable,
		},
		{
			name: "status is false",
			addOnConditions: []metav1.Condition{
				{
					Type:   addonv1alpha1.ManagedClusterAddOnConditionAvailable,
					Status: metav1.ConditionFalse,
				},
			},
			expectedValue: addOnStatusUnhealthy,
		},
		{
			name: "status is unknow",
			addOnConditions: []metav1.Condition{
				{
					Type:   addonv1alpha1.ManagedClusterAddOnConditionAvailable,
					Status: metav1.ConditionUnknown,
				},
			},
			expectedValue: addOnStatusUnreachable,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addOn := &addonv1alpha1.ManagedClusterAddOn{
				Status: addonv1alpha1.ManagedClusterAddOnStatus{
					Conditions: c.addOnConditions,
				},
			}

			value := getAddOnLabelValue(addOn)
			if c.expectedValue != value {
				t.Errorf("expected %q but get %q", c.expectedValue, value)
			}
		})
	}
}

func TestDiscoveryController_SyncAddOn(t *testing.T) {
	clusterName := "cluster1"
	deleteTime := metav1.Now()

	cases := []struct {
		name            string
		addOnName       string
		cluster         *clusterv1.ManagedCluster
		addOn           *addonv1alpha1.ManagedClusterAddOn
		validateActions func(t *testing.T, actions []clienttesting.Action)
	}{
		{
			name:      "addon is deleted",
			addOnName: "addon1",
			cluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
					Labels: map[string]string{
						"feature.open-cluster-management.io/addon-addon1": addOnStatusAvailable,
					},
				},
			},
			validateActions: func(t *testing.T, actions []clienttesting.Action) {
				testinghelpers.AssertActions(t, actions, "patch")
				actualPatchAction := actions[0].(clienttesting.PatchActionImpl)
				assertPatchNoAddonLabel(t, actualPatchAction, "addon1")
			},
		},
		{
			name:      "addon is deleting",
			addOnName: "addon1",
			cluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
				},
			},
			addOn: &addonv1alpha1.ManagedClusterAddOn{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "addon1",
					Namespace:         clusterName,
					DeletionTimestamp: &deleteTime,
				},
			},
			validateActions: testinghelpers.AssertNoActions,
		},
		{
			name:      "new addon is added",
			addOnName: "addon1",
			cluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
				},
			},
			addOn: &addonv1alpha1.ManagedClusterAddOn{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "addon1",
					Namespace: clusterName,
				},
			},
			validateActions: func(t *testing.T, actions []clienttesting.Action) {
				testinghelpers.AssertActions(t, actions, "patch")
				actualPatchAction := actions[0].(clienttesting.PatchActionImpl)
				assertPatchAddonLabel(t, actualPatchAction, "addon1", addOnStatusUnreachable)
			},
		},
		{
			name:      "addon status is updated",
			addOnName: "addon1",
			cluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
					Labels: map[string]string{
						"feature.open-cluster-management.io/addon-addon1": addOnStatusAvailable,
					},
				},
			},
			addOn: &addonv1alpha1.ManagedClusterAddOn{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "addon1",
					Namespace: clusterName,
				},
			},
			validateActions: func(t *testing.T, actions []clienttesting.Action) {
				testinghelpers.AssertActions(t, actions, "patch")
				actualPatchAction := actions[0].(clienttesting.PatchActionImpl)
				assertPatchAddonLabel(t, actualPatchAction, "addon1", addOnStatusUnreachable)
			},
		},
		{
			name:      "cluster is deleting",
			addOnName: "addon1",
			cluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              clusterName,
					DeletionTimestamp: &deleteTime,
				},
			},
			addOn: &addonv1alpha1.ManagedClusterAddOn{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "addon1",
					Namespace: clusterName,
				},
			},
			validateActions: testinghelpers.AssertNoActions,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			objs := []runtime.Object{}
			if c.cluster != nil {
				objs = append(objs, c.cluster)
			}

			clusterClient := clusterfake.NewSimpleClientset(objs...)

			clusterInformerFactory := clusterinformers.NewSharedInformerFactory(clusterClient, time.Minute*10)
			if c.cluster != nil {
				clusterStore := clusterInformerFactory.Cluster().V1().ManagedClusters().Informer().GetStore()
				if err := clusterStore.Add(c.cluster); err != nil {
					t.Fatal(err)
				}
			}

			objs = []runtime.Object{}
			if c.addOn != nil {
				objs = append(objs, c.addOn)
			}
			addOnClient := addonfake.NewSimpleClientset(objs...)
			addOnInformerFactory := addoninformers.NewSharedInformerFactoryWithOptions(addOnClient, 10*time.Minute)
			if c.addOn != nil {
				addOnStore := addOnInformerFactory.Addon().V1alpha1().ManagedClusterAddOns().Informer().GetStore()
				if err := addOnStore.Add(c.addOn); err != nil {
					t.Fatal(err)
				}
			}

			controller := addOnFeatureDiscoveryController{
				clusterClient: clusterClient,
				clusterLister: clusterInformerFactory.Cluster().V1().ManagedClusters().Lister(),
				addOnLister:   addOnInformerFactory.Addon().V1alpha1().ManagedClusterAddOns().Lister(),
			}

			err := controller.syncAddOn(context.Background(), clusterName, c.addOnName)
			if err != nil {
				t.Errorf("unexpected err: %v", err)
			}

			c.validateActions(t, clusterClient.Actions())
		})
	}
}

func TestDiscoveryController_Sync(t *testing.T) {
	clusterName := "cluster1"
	deleteTime := metav1.Now()

	cases := []struct {
		name            string
		queueKey        string
		cluster         *clusterv1.ManagedCluster
		addOns          []*addonv1alpha1.ManagedClusterAddOn
		validateActions func(t *testing.T, actions []clienttesting.Action)
	}{
		{
			name:     "addon synced",
			queueKey: "cluster1/addon1",
			cluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
				},
			},
			addOns: []*addonv1alpha1.ManagedClusterAddOn{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "addon1",
						Namespace: clusterName,
					},
				},
			},
			validateActions: func(t *testing.T, actions []clienttesting.Action) {
				testinghelpers.AssertActions(t, actions, "patch")
				actualPatchAction := actions[0].(clienttesting.PatchActionImpl)
				assertPatchAddonLabel(t, actualPatchAction, "addon1", addOnStatusUnreachable)
			},
		},
		{
			name:            "cluster not found",
			queueKey:        clusterName,
			validateActions: testinghelpers.AssertNoActions,
		},
		{
			name:     "cluster is deleting",
			queueKey: clusterName,
			cluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              clusterName,
					DeletionTimestamp: &deleteTime,
				},
			},
			validateActions: testinghelpers.AssertNoActions,
		},
		{
			name:     "no change",
			queueKey: clusterName,
			cluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
				},
			},
			validateActions: testinghelpers.AssertNoActions,
		},
		{
			name:     "cluster synced",
			queueKey: clusterName,
			cluster: &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
					Labels: map[string]string{
						"feature.open-cluster-management.io/addon-addon4": "available",
					},
				},
			},
			addOns: []*addonv1alpha1.ManagedClusterAddOn{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "addon1",
						Namespace: clusterName,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "addon2",
						Namespace:         clusterName,
						DeletionTimestamp: &deleteTime,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "addon3",
						Namespace: clusterName,
					},
					Status: addonv1alpha1.ManagedClusterAddOnStatus{
						Conditions: []metav1.Condition{
							{
								Type:   addonv1alpha1.ManagedClusterAddOnConditionAvailable,
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
			},
			validateActions: func(t *testing.T, actions []clienttesting.Action) {
				testinghelpers.AssertActions(t, actions, "patch")
				actualPatchAction := actions[0].(clienttesting.PatchActionImpl)
				assertPatchAddonLabel(t, actualPatchAction, "addon1", addOnStatusUnreachable)
				assertPatchAddonLabel(t, actualPatchAction, "addon3", addOnStatusAvailable)
				assertPatchNoAddonLabel(t, actualPatchAction, "addon4")
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			objs := []runtime.Object{}
			if c.cluster != nil {
				objs = append(objs, c.cluster)
			}

			clusterClient := clusterfake.NewSimpleClientset(objs...)

			clusterInformerFactory := clusterinformers.NewSharedInformerFactory(clusterClient, time.Minute*10)
			if c.cluster != nil {
				clusterStore := clusterInformerFactory.Cluster().V1().ManagedClusters().Informer().GetStore()
				if err := clusterStore.Add(c.cluster); err != nil {
					t.Fatal(err)
				}
			}

			objs = []runtime.Object{}
			for _, addOn := range c.addOns {
				objs = append(objs, addOn)
			}
			addOnClient := addonfake.NewSimpleClientset(objs...)
			addOnInformerFactory := addoninformers.NewSharedInformerFactoryWithOptions(addOnClient, 10*time.Minute)
			addOnStore := addOnInformerFactory.Addon().V1alpha1().ManagedClusterAddOns().Informer().GetStore()
			for _, addOn := range c.addOns {
				if err := addOnStore.Add(addOn); err != nil {
					t.Fatal(err)
				}
			}

			controller := addOnFeatureDiscoveryController{
				clusterClient: clusterClient,
				clusterLister: clusterInformerFactory.Cluster().V1().ManagedClusters().Lister(),
				addOnLister:   addOnInformerFactory.Addon().V1alpha1().ManagedClusterAddOns().Lister(),
			}

			err := controller.sync(context.Background(), testinghelpers.NewFakeSyncContext(t, c.queueKey))
			if err != nil {
				t.Errorf("unexpected err: %v", err)
			}

			c.validateActions(t, clusterClient.Actions())
		})
	}
}

func assertPatchAddonLabel(t *testing.T, actionPatch clienttesting.PatchActionImpl, addOnName, addOnStatus string) {
	var patchObj map[string]map[string]map[string]string
	if err := json.Unmarshal(actionPatch.Patch, &patchObj); err != nil {
		t.Errorf("failed to unmarshal patch %s: %v", patchObj, err)
	}
	metadata, ok := patchObj["metadata"]
	if !ok {
		t.Errorf("patch %s doesn't contain metadata field", patchObj)
	}
	labels, ok := metadata["labels"]
	if !ok {
		t.Errorf("patch %s doesn't contain metadata.labels field", patchObj)
	}

	key := fmt.Sprintf("%s%s", addOnFeaturePrefix, addOnName)
	value, ok := labels[key]
	if !ok {
		t.Errorf("label %q not found", key)
	}

	if value != addOnStatus {
		t.Errorf("expect label value %q but found %q", addOnStatus, value)
	}
}

func assertPatchNoAddonLabel(t *testing.T, actionPatch clienttesting.PatchActionImpl, addOnName string) {
	var patchObj map[string]map[string]map[string]string
	if err := json.Unmarshal(actionPatch.Patch, &patchObj); err != nil {
		t.Errorf("failed to unmarshal patch %s: %v", patchObj, err)
	}
	metadata, ok := patchObj["metadata"]
	if !ok {
		t.Errorf("patch %s doesn't contain metadata field", patchObj)
	}
	labels, ok := metadata["labels"]
	if !ok {
		t.Errorf("patch %s doesn't contain metadata.labels field", patchObj)
	}

	key := fmt.Sprintf("%s%s", addOnFeaturePrefix, addOnName)
	if _, ok := labels[key]; ok {
		t.Errorf("label %q found", key)
	}
}
