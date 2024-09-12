package batchscheduler

import (
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"

	configapi "github.com/ray-project/kuberay/ray-operator/apis/config/v1alpha1"
	"github.com/ray-project/kuberay/ray-operator/controllers/ray/batchscheduler/volcano"
	"github.com/ray-project/kuberay/ray-operator/controllers/ray/batchscheduler/yunikorn"

	"k8s.io/client-go/rest"

	rayv1 "github.com/ray-project/kuberay/ray-operator/apis/ray/v1"
	schedulerinterface "github.com/ray-project/kuberay/ray-operator/controllers/ray/batchscheduler/interface"
	"github.com/ray-project/kuberay/ray-operator/controllers/ray/utils"
)

type SchedulerManager struct {
	config     *rest.Config
	factory    schedulerinterface.BatchSchedulerFactory
	scheduler  schedulerinterface.BatchScheduler
	rayConfigs configapi.Configuration
	sync.Mutex
}

// NewSchedulerManager maintains a specific scheduler plugin based on config
func NewSchedulerManager(rayConfigs configapi.Configuration, config *rest.Config) (*SchedulerManager, error) {
	// init the scheduler factory from config
	factory := getSchedulerFactory(rayConfigs)
	scheduler, err := factory.New(config)
	if err != nil {
		return nil, err
	}

	manager := SchedulerManager{
		rayConfigs: rayConfigs,
		config:     config,
		factory:    factory,
		scheduler:  scheduler,
	}

	return &manager, nil
}

func getSchedulerFactory(rayConfigs configapi.Configuration) schedulerinterface.BatchSchedulerFactory {
	var factory schedulerinterface.BatchSchedulerFactory
	// init with the default factory
	factory = &schedulerinterface.DefaultBatchSchedulerFactory{}
	// when a batch scheduler name is provided
	if len(rayConfigs.BatchScheduler) > 0 {
		switch rayConfigs.BatchScheduler {
		case volcano.GetPluginName():
			factory = &volcano.VolcanoBatchSchedulerFactory{}
		case yunikorn.GetPluginName():
			factory = &yunikorn.YuniKornSchedulerFactory{}
		default:
			factory = &schedulerinterface.DefaultBatchSchedulerFactory{}
		}
	}

	// legacy option, if this is enabled, register volcano
	// this is for backwards compatibility
	if rayConfigs.EnableBatchScheduler {
		factory = &volcano.VolcanoBatchSchedulerFactory{}
	}

	return factory
}

func (batch *SchedulerManager) GetSchedulerForCluster(app *rayv1.RayCluster) (schedulerinterface.BatchScheduler, error) {
	// for backwards compatibility
	if batch.rayConfigs.EnableBatchScheduler {
		if schedulerName, ok := app.ObjectMeta.Labels[utils.RaySchedulerName]; ok {
			if schedulerName == volcano.GetPluginName() {
				return batch.scheduler, nil
			}
		}
	}

	return batch.scheduler, nil
}

func (batch *SchedulerManager) ConfigureReconciler(b *builder.Builder) *builder.Builder {
	batch.factory.ConfigureReconciler(b)
	return b
}

func (batch *SchedulerManager) AddToScheme(scheme *runtime.Scheme) {
	batch.factory.AddToScheme(scheme)
}
