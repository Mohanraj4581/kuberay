package schedulerinterface

import (
	rayiov1alpha1 "github.com/ray-project/kuberay/ray-operator/apis/ray/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/builder"
)

type BatchScheduler interface {
	Name() string
	DoBatchSchedulingOnSubmission(app *rayiov1alpha1.RayCluster) error
	AddMetadataToPod(app *rayiov1alpha1.RayCluster, pod *v1.Pod)
}

type BatchSchedulerFactory interface {
	New(config *rest.Config) (BatchScheduler, error)
	AddToScheme(scheme *runtime.Scheme)
	ConfigureReconciler(b *builder.Builder) *builder.Builder
}

type DefaultBatchScheduler struct{}

type DefaultBatchSchedulerFactory struct{}

func GetDefaultPluginName() string {
	return "default"
}

func (d *DefaultBatchScheduler) Name() string {
	return GetDefaultPluginName()
}

func (d *DefaultBatchScheduler) DoBatchSchedulingOnSubmission(app *rayiov1alpha1.RayCluster) error {
	return nil
}

func (d *DefaultBatchScheduler) AddMetadataToPod(app *rayiov1alpha1.RayCluster, pod *v1.Pod) {
}

func (df *DefaultBatchSchedulerFactory) New(config *rest.Config) (BatchScheduler, error) {
	return &DefaultBatchScheduler{}, nil
}

func (df *DefaultBatchSchedulerFactory) AddToScheme(scheme *runtime.Scheme) {
}

func (df *DefaultBatchSchedulerFactory) ConfigureReconciler(b *builder.Builder) *builder.Builder {
	return b
}
