/*
Copyright 2021.

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

package controllers

import (
	"context"
	"errors"
	"time"

	"github.com/go-logr/logr"

	machinev1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	v1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/medik8s/poison-pill/api/v1alpha1"
	"github.com/medik8s/poison-pill/pkg/reboot"
	"github.com/medik8s/poison-pill/pkg/utils"
)

const (
	pprFinalizer = "poison-pill.medik8s.io/ppr-finalizer"
)

var (
	NodeUnschedulableTaint = &v1.Taint{
		Key:    "node.kubernetes.io/unschedulable",
		Effect: v1.TaintEffectNoSchedule,
	}
)

// PoisonPillRemediationReconciler reconciles a PoisonPillRemediation object
type PoisonPillRemediationReconciler struct {
	client.Client
	Log logr.Logger
	//logger is a logger that holds the CR name being reconciled
	logger   logr.Logger
	Scheme   *runtime.Scheme
	Rebooter reboot.Rebooter
	// note that this time must include the time for a unhealthy node without api-server access to reach the conclusion that it's unhealthy
	// this should be at least worst-case time to reach a conclusion from the other peers * request context timeout + watchdog interval + maxFailuresThreshold * reconcileInterval + padding
	SafeTimeToAssumeNodeRebooted time.Duration
	MyNodeName                   string
}

// SetupWithManager sets up the controller with the Manager.
func (r *PoisonPillRemediationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PoisonPillRemediation{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=poison-pill.medik8s.io,resources=poisonpillremediations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=poison-pill.medik8s.io,resources=poisonpillremediations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=poison-pill.medik8s.io,resources=poisonpillremediations/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=machine.openshift.io,resources=machines,verbs=get;list;watch

func (r *PoisonPillRemediationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = r.Log.WithValues("poisonpillremediation", req.NamespacedName)

	ppr := &v1alpha1.PoisonPillRemediation{}
	if err := r.Get(ctx, req.NamespacedName, ppr); err != nil {
		if apiErrors.IsNotFound(err) {
			// PPR is deleted, stop reconciling
			r.logger.Info("PPR already deleted")
			return ctrl.Result{}, nil
		}
		r.logger.Error(err, "failed to get PPR")
		return ctrl.Result{}, err
	}

	node, err := r.getNodeFromPpr(ppr)
	if err != nil {
		if apiErrors.IsNotFound(err) {
			//as part of the remediation flow, we delete the node, and then we need to restore it
			return r.handleDeletedNode(ppr)
		}
		r.logger.Error(err, "failed to get node", "node name", ppr.Name)
		return ctrl.Result{}, err
	}

	if node.CreationTimestamp.After(ppr.CreationTimestamp.Time) {
		//this node was created after the node was reported as unhealthy
		//we assume this is the new node after remediation and take no-op expecting the ppr to be deleted
		if ppr.Status.NodeBackup != nil {
			//TODO: this is an ugly hack. without it the api-server complains about
			//missing apiVersion and Kind for the nodeBackup.
			ppr.Status.NodeBackup = nil
			if err := r.Client.Status().Update(context.Background(), ppr); err != nil {
				r.logger.Error(err, "failed to remove node backup from ppr")
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}

		r.logger.Info("node has been restored", "node name", node.Name)

		//remove finalizer as remediation completed (successfully or not)
		if controllerutil.ContainsFinalizer(ppr, pprFinalizer) {
			controllerutil.RemoveFinalizer(ppr, pprFinalizer)
			if err := r.Client.Update(context.Background(), ppr); err != nil {
				r.logger.Error(err, "failed to remove finalizer from ppr")
				return ctrl.Result{}, err
			}
		}

		//todo this means we only allow one remediation attempt per ppr. we could add some config to
		//ppr which states max remediation attempts, and the timeout to consider a remediation failed.
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(ppr, pprFinalizer) {
		if !ppr.DeletionTimestamp.IsZero() {
			//ppr is going to be deleted before we started any remediation action, so taking no-op
			//otherwise we continue the remediation even if the deletionTimestamp is not zero
			r.logger.Info("ppr is about to be deleted, which means the resource is healthy again. taking no-op")
			return ctrl.Result{}, nil
		}

		controllerutil.AddFinalizer(ppr, pprFinalizer)
		if err := r.Client.Update(context.Background(), ppr); err != nil {
			r.logger.Error(err, "failed to add finalizer to ppr")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if !node.Spec.Unschedulable {
		//the unhealthy node might reboot itself and take new workloads
		//since we're going to delete the node eventually, we must make sure the node is deleted
		//when there's no running workload there. Hence we mark it as unschedulable.
		return r.markNodeAsUnschedulable(node)
	}

	if !utils.TaintExists(node.Spec.Taints, NodeUnschedulableTaint) {
		r.logger.Info("waiting for unschedulable taint to appear", "node name", node.Name)
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	if ppr.Status.NodeBackup == nil || ppr.Status.TimeAssumedRebooted.IsZero() {
		return r.updatePprStatus(node, ppr)
	}

	maxNodeRebootTime := ppr.Status.TimeAssumedRebooted

	if maxNodeRebootTime.After(time.Now()) {
		if r.MyNodeName == node.Name {
			// we have a problem on this node
			if err := r.Rebooter.Reboot(); err != nil {
				// re-queue
				return ctrl.Result{}, err
			} else {
				// we are done for now, node will reboot
				return ctrl.Result{}, nil
			}
		}
		return ctrl.Result{RequeueAfter: maxNodeRebootTime.Sub(time.Now()) + time.Second}, nil
	}

	r.logger.Info("TimeAssumedRebooted is old. The unhealthy node assumed to been rebooted", "node name", node.Name)

	if !node.DeletionTimestamp.IsZero() {
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	r.logger.Info("deleting unhealthy node", "node name", node.Name)
	if err := r.Client.Delete(context.TODO(), node); err != nil {
		if !apiErrors.IsNotFound(err) {
			r.logger.Error(err, "failed to delete the unhealthy node")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
}

func (r *PoisonPillRemediationReconciler) updatePprStatus(node *v1.Node, ppr *v1alpha1.PoisonPillRemediation) (ctrl.Result, error) {
	r.logger.Info("updating ppr with node backup and updating time to assume node has been rebooted", "node name", node.Name)
	//we assume the unhealthy node will be rebooted by maxTimeNodeHasRebooted
	maxTimeNodeHasRebooted := metav1.NewTime(metav1.Now().Add(r.SafeTimeToAssumeNodeRebooted))
	ppr.Status.TimeAssumedRebooted = &maxTimeNodeHasRebooted
	ppr.Status.NodeBackup = node
	ppr.Status.NodeBackup.Kind = node.GetObjectKind().GroupVersionKind().Kind
	ppr.Status.NodeBackup.APIVersion = node.APIVersion

	err := r.Client.Status().Update(context.Background(), ppr)
	if err != nil {
		if apiErrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}
		r.logger.Error(err, "failed to update status with 'node back up' and 'time to assume node has rebooted'")
		return ctrl.Result{}, err
	}

	return ctrl.Result{Requeue: true}, nil
}

// getNodeFromPpr returns the unhealthy node reported in the given ppr
func (r *PoisonPillRemediationReconciler) getNodeFromPpr(ppr *v1alpha1.PoisonPillRemediation) (*v1.Node, error) {
	//PPR could be created by either machine based controller (e.g. MHC) or
	//by a node based controller (e.g. NHC). This assumes that machine based controller
	//will create the ppr with machine owner reference

	for _, ownerRef := range ppr.OwnerReferences {
		if ownerRef.Kind == "Machine" {
			return r.getNodeFromMachine(ownerRef, ppr.Namespace)
		}
	}

	//since we didn't find a machine owner ref, we assume that ppr name is the unhealthy node name
	node := &v1.Node{}
	key := client.ObjectKey{
		Name:      ppr.Name,
		Namespace: "",
	}

	if err := r.Get(context.TODO(), key, node); err != nil {
		return nil, err
	}

	return node, nil
}

func (r *PoisonPillRemediationReconciler) getNodeFromMachine(ref metav1.OwnerReference, ns string) (*v1.Node, error) {
	machine := &machinev1beta1.Machine{}
	machineKey := client.ObjectKey{
		Name:      ref.Name,
		Namespace: ns,
	}

	if err := r.Client.Get(context.Background(), machineKey, machine); err != nil {
		r.logger.Error(err, "failed to get machine from PoisonPillRemediation CR owner ref",
			"machine name", machineKey.Name, "namespace", machineKey.Namespace)
		return nil, err
	}

	if machine.Status.NodeRef == nil {
		err := errors.New("nodeRef is nil")
		r.logger.Error(err, "failed to retrieve node from the unhealthy machine")
		return nil, err
	}

	node := &v1.Node{}
	key := client.ObjectKey{
		Name:      machine.Status.NodeRef.Name,
		Namespace: machine.Status.NodeRef.Namespace,
	}

	if err := r.Get(context.Background(), key, node); err != nil {
		r.logger.Error(err, "failed to retrieve node from the unhealthy machine",
			"node name", node.Name, "machine name", machine.Name)
		return nil, err
	}

	return node, nil
}

func (r *PoisonPillRemediationReconciler) markNodeAsUnschedulable(node *v1.Node) (ctrl.Result, error) {
	node.Spec.Unschedulable = true
	r.logger.Info("Marking node as unschedulable", "node name", node.Name)
	if err := r.Client.Update(context.Background(), node); err != nil {
		r.logger.Error(err, "failed to mark node as unschedulable")
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
}

func (r *PoisonPillRemediationReconciler) handleDeletedNode(ppr *v1alpha1.PoisonPillRemediation) (ctrl.Result, error) {
	if ppr.Status.NodeBackup == nil {
		err := errors.New("unhealthy node doesn't exist and there's no backup node to restore")
		r.logger.Error(err, "remediation failed")
		// there is nothing we can do about it, stop reconciling
		return ctrl.Result{}, nil
	}

	return r.restoreNode(ppr.Status.NodeBackup)
}

func (r *PoisonPillRemediationReconciler) restoreNode(nodeToRestore *v1.Node) (ctrl.Result, error) {
	r.logger.Info("restoring node", "node name", nodeToRestore.Name)

	// todo we probably want to have some allowlist/denylist on which things to restore, we already had
	// a problem when we restored ovn annotations
	nodeToRestore.ResourceVersion = "" //create won't work with a non-empty value here
	taints, _ := utils.DeleteTaint(nodeToRestore.Spec.Taints, NodeUnschedulableTaint)
	nodeToRestore.Spec.Taints = taints
	nodeToRestore.Spec.Unschedulable = false
	nodeToRestore.CreationTimestamp = metav1.Now()

	//todo should we also delete conditions that made the node reach the unhealthy state?
	//I'm afraid we might cause remediation loops

	if err := r.Client.Create(context.TODO(), nodeToRestore); err != nil {
		if apiErrors.IsAlreadyExists(err) {
			r.logger.Info("failed to restore node, already exists again")
			// there is nothing we can do about it, stop reconciling
			return ctrl.Result{}, nil
		}
		r.logger.Error(err, "failed to create node", "node name", nodeToRestore.Name)
		return ctrl.Result{}, err
	}

	// all done, stop reconciling
	return ctrl.Result{}, nil
}
