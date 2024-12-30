/*
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

package interruption

import (
	"context"
	"fmt"
	"time"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/events"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/cache"
	interruptionevents "github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/controllers/interruption/events"
)

const (
	ConditionTypeInstanceExpired = "InstanceExpired"
)

// Controller is an AlibabaCloud interruption controller.
type Controller struct {
	kubeClient client.Client
	recorder   events.Recorder

	unavailableOfferingsCache *cache.UnavailableOfferings
}

func NewController(kubeClient client.Client, recorder events.Recorder,
	unavailableOfferingsCache *cache.UnavailableOfferings) *Controller {
	return &Controller{
		kubeClient: kubeClient,
		recorder:   recorder,

		unavailableOfferingsCache: unavailableOfferingsCache,
	}
}

func (c *Controller) Reconcile(ctx context.Context, node *corev1.Node) (reconcile.Result, error) {
	nodeConditions := node.Status.Conditions

	nodeCondition, interrupted := lo.Find(nodeConditions, func(condition corev1.NodeCondition) bool {
		return condition.Type == ConditionTypeInstanceExpired && condition.Status == corev1.ConditionTrue
	})

	if !interrupted {
		return reconcile.Result{}, nil
	}

	nodeClaim, err := c.getNodeClaimByNodeName(ctx, node.Name)
	if err != nil {
		return reconcile.Result{RequeueAfter: time.Second * 5}, err
	}

	zone := nodeClaim.Labels[corev1.LabelTopologyZone]
	instanceType := nodeClaim.Labels[corev1.LabelInstanceTypeStable]
	if zone != "" && instanceType != "" {
		c.unavailableOfferingsCache.MarkUnavailable(ctx, nodeCondition.Reason, instanceType, zone, karpv1.CapacityTypeSpot)
	}

	if err := c.deleteNodeClaim(ctx, nodeClaim, node); err != nil {
		return reconcile.Result{RequeueAfter: time.Second * 5}, err
	}

	return reconcile.Result{}, nil
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("interruption").
		For(&corev1.Node{}).
		WithEventFilter(predicate.NewTypedPredicateFuncs(func(obj client.Object) bool {
			if label, ok := obj.GetLabels()[karpv1.CapacityTypeLabelKey]; !ok || label != karpv1.CapacityTypeSpot {
				return false
			}

			return true
		})).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}

// deleteNodeClaim removes the NodeClaim from the api-server
func (c *Controller) deleteNodeClaim(ctx context.Context, nodeClaim *karpv1.NodeClaim, node *corev1.Node) error {
	if !nodeClaim.DeletionTimestamp.IsZero() {
		return nil
	}
	if err := c.kubeClient.Delete(ctx, nodeClaim); err != nil {
		return client.IgnoreNotFound(fmt.Errorf("deleting the node on interruption message, %w", err))
	}
	log.FromContext(ctx).Info("initiating delete from interruption message")
	c.recorder.Publish(interruptionevents.TerminatingOnInterruption(node, nodeClaim)...)
	return nil
}

func (c *Controller) getNodeClaimByNodeName(ctx context.Context, nodeName string) (*karpv1.NodeClaim, error) {
	nodeClaimList := &karpv1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, nodeClaimList); err != nil {
		return nil, err
	}

	for ni := range nodeClaimList.Items {
		if nodeClaimList.Items[ni].Status.NodeName == nodeName {
			return nodeClaimList.Items[ni].DeepCopy(), nil
		}
	}

	return nil, fmt.Errorf("no nodeclaim found for node %s", nodeName)
}
