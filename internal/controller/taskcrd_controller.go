/*
Copyright 2024.

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

package controller

import (
	"context"

	v1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	webappv1 "xiaoyeshiyu.domain/taskcrd/api/v1"
)

// TaskCrdReconciler reconciles a TaskCrd object
type TaskCrdReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=webapp.xiaoyeshiyu.domain,resources=taskcrds,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=webapp.xiaoyeshiyu.domain,resources=taskcrds/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=webapp.xiaoyeshiyu.domain,resources=taskcrds/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the TaskCrd object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *TaskCrdReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	var taskCrd webappv1.TaskCrd
	// 获取 guestbook 对象
	err := r.Client.Get(ctx, req.NamespacedName, &taskCrd)
	if err != nil {
		return ctrl.Result{}, err
	}

	if taskCrd.Spec.Deploy != "" {
		var deploys v1.DeploymentList
		err = r.Client.List(ctx, &deploys)
		if err != nil {
			return ctrl.Result{}, err
		}

		for _, deploy := range deploys.Items {
			if deploy.ObjectMeta.Name == taskCrd.Spec.Deploy {
				deploy.Spec.Replicas = &taskCrd.Spec.Replicas
				err = r.Client.Update(ctx, &deploy)
				if err != nil {
					return ctrl.Result{}, err
				}
			}
		}

	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TaskCrdReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webappv1.TaskCrd{}).
		Complete(r)
}
