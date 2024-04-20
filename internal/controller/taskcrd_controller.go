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
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
	v1 "k8s.io/api/apps/v1"
	v2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
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

	// 获取 CRD
	var taskCrd webappv1.TaskCrd
	err := r.Client.Get(ctx, req.NamespacedName, &taskCrd)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 获取 deployment
	if taskCrd.Spec.Deployment == "" {
		return ctrl.Result{}, nil
	}

	var deploy v1.Deployment
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: taskCrd.Namespace, Name: taskCrd.Spec.Deployment}, &deploy)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 获取 hpa
	if taskCrd.Spec.HPA == "" {
		return ctrl.Result{}, nil
	}

	hpa := v2.HorizontalPodAutoscaler{}
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: taskCrd.Spec.HPA}, &hpa)
	if err != nil {
		return ctrl.Result{}, err
	}

	var (
		start = make(chan struct{})
		end   = make(chan struct{})
	)
	// 都存在，则开始执行
	c := cron.New()
	c.AddFunc(taskCrd.Spec.Start, func() {
		start <- struct{}{}
	})

	c.AddFunc(taskCrd.Spec.End, func() {
		end <- struct{}{}
	})

	for {
	OUT:
		select {
		case <-ctx.Done():
			return ctrl.Result{}, ctx.Err()
		case <-start:
			log.Log.Info("task start: ", time.Now())
			tick := time.NewTicker(10 * time.Second)
			for {
				select {
				case <-ctx.Done():
					return ctrl.Result{}, ctx.Err()
				case <-end:
					log.Log.Info("task end: ", time.Now())
					atomic.StoreInt32(deploy.Spec.Replicas, taskCrd.Spec.EndReplicas)
					err = r.Client.Update(ctx, &deploy)
					if err != nil {
						return ctrl.Result{}, err
					}
					break OUT
				case <-tick.C:
					err = r.Client.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: taskCrd.Spec.HPA}, &hpa)
					if err != nil {
						return ctrl.Result{}, err
					}
					for _, metrics := range hpa.Status.CurrentMetrics {
						// CPU 的平均使用率小于设定值，则修改对应 deployment 副本数目
						if metrics.Resource.Name == corev1.ResourceCPU && *metrics.Resource.Current.AverageUtilization < taskCrd.Spec.AverageUtilization {
							err = r.Client.Get(ctx, client.ObjectKey{Namespace: taskCrd.Namespace, Name: taskCrd.Spec.Deployment}, &deploy)
							if err != nil {
								return ctrl.Result{}, err
							}
							log.Log.Info("get deploy", deploy.Name, "replicas", deploy.Status.Replicas)
							if deploy.Status.Replicas < taskCrd.Spec.MaxReplicas {
								atomic.AddInt32(deploy.Spec.Replicas, 1)
								err = r.Client.Update(ctx, &deploy)
								if err != nil {
									return ctrl.Result{}, err
								}
							} else {
								// 不小于，则结束
								tick.Stop()
							}
						}
					}
				}
			}
		}
	}

	c.Stop()

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TaskCrdReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webappv1.TaskCrd{}).
		Complete(r)
}
