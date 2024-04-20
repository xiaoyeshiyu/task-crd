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

	Tasks map[string]Task
}

type Task struct {
	name  string
	cron  *cron.Cron
	start chan struct{}
	end   chan struct{}
	done  chan struct{}
}

func name(namespace string, name string) string {
	return namespace + "." + name
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

	log.Log.Info("task-crd", "request", req)

	// 获取 CRD
	var taskCrd webappv1.TaskCrd
	err := r.Client.Get(ctx, req.NamespacedName, &taskCrd)
	if err != nil {
		log.Log.Error(err, "get task crd")
		// delete crd
		if task, ok := r.Tasks[name(taskCrd.Namespace, taskCrd.Name)]; ok {
			task.stop(ctx)
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, nil
	}

	log.Log.Info("task-crd", "taskCrd Info", taskCrd)

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

	// 获取任务
	if task, ok := r.Tasks[taskCrd.Namespace+taskCrd.Name]; ok {
		task.stop(ctx)
	}

	task := Task{
		name:  name(taskCrd.Namespace, taskCrd.Name),
		cron:  cron.New(),
		start: make(chan struct{}),
		end:   make(chan struct{}),
		done:  make(chan struct{}),
	}
	r.Tasks[task.name] = task

	// add new job
	_, err = task.cron.AddFunc(taskCrd.Spec.Start, func() {
		log.Log.Info("crontab", "start", time.Now())
		task.start <- struct{}{}
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	_, err = task.cron.AddFunc(taskCrd.Spec.End, func() {
		log.Log.Info("crontab", "end", time.Now())
		task.end <- struct{}{}
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	go task.do(ctx, r.Client, taskCrd)

	task.cron.Start()

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TaskCrdReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webappv1.TaskCrd{}).
		Complete(r)
}

func (t *Task) stop(ctx context.Context) {
	log.Log.Info("task", "stop", t.name)
	// clean all cron job
	for _, entry := range t.cron.Entries() {
		t.cron.Remove(entry.ID)
	}
	close(t.done)
	t.cron.Stop()
}

func (t *Task) do(ctx context.Context, c client.Client, taskCrd webappv1.TaskCrd) {
	log.Log.Info("do start")
	for {
	OUT:
		select {
		case <-t.done:
			log.Log.Info("do", "task done", t.name)
			return
		case <-ctx.Done():
			log.Log.Info("do", "context done", t.name)
			return
		case <-t.start:
			log.Log.Info("do", "start", time.Now(), "task", t.name)
			tick := time.NewTicker(10 * time.Second)

			var deploy v1.Deployment
			err := c.Get(ctx, client.ObjectKey{Namespace: taskCrd.Namespace, Name: taskCrd.Spec.Deployment}, &deploy)
			if err != nil {
				return
			}

			for {
				select {
				case <-t.done:
					log.Log.Info("do", "task done", t.name)
					return
				case <-ctx.Done():
					log.Log.Info("do", "context done", t.name)
					return
				case <-t.end:
					log.Log.Info("task", "end", time.Now())
					err := c.Get(ctx, client.ObjectKey{Namespace: taskCrd.Namespace, Name: taskCrd.Spec.Deployment}, &deploy)
					if err != nil {
						return
					}
					atomic.StoreInt32(deploy.Spec.Replicas, taskCrd.Spec.EndReplicas)
					err = c.Update(ctx, &deploy)
					if err != nil {
						log.Log.Info("task", "end", err)
						return
					}
					break OUT
				case <-tick.C:
					var hpa v2.HorizontalPodAutoscaler
					err = c.Get(ctx, client.ObjectKey{Namespace: taskCrd.Namespace, Name: taskCrd.Spec.HPA}, &hpa)
					if err != nil {
						return
					}
					for _, metrics := range hpa.Status.CurrentMetrics {
						// CPU 的平均使用率小于设定值，则修改对应 deployment 副本数目
						if metrics.Resource.Name == corev1.ResourceCPU && *metrics.Resource.Current.AverageUtilization < taskCrd.Spec.AverageUtilization {
							err = c.Get(ctx, client.ObjectKey{Namespace: taskCrd.Namespace, Name: taskCrd.Spec.Deployment}, &deploy)
							if err != nil {
								return
							}
							log.Log.Info("task", "deployment", deploy.Name, "replicas", deploy.Status.Replicas)
							if deploy.Status.Replicas < taskCrd.Spec.MaxReplicas {
								atomic.AddInt32(deploy.Spec.Replicas, 1)
								err = c.Update(ctx, &deploy)
								if err != nil {
									return
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
}
