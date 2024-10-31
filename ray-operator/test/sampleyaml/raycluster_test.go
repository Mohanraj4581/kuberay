package sampleyaml

import (
	"testing"

	"github.com/onsi/gomega"

	rayv1 "github.com/ray-project/kuberay/ray-operator/apis/ray/v1"
	. "github.com/ray-project/kuberay/ray-operator/test/support"
)

func TestRayCluster(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "ray-cluster.autoscaler-v2.yaml",
		},
		{
			name: "ray-cluster.autoscaler.yaml",
		},
		{
			name: "ray-cluster.complete.yaml",
		},
		{
			name: "ray-cluster.custom-head-service.yaml",
		},
		{
			name: "ray-cluster.embed-grafana.yaml",
		},
		{
			name: "ray-cluster.external-redis-uri.yaml",
		},
		{
			name: "ray-cluster.external-redis.yaml",
		},
		{
			name: "ray-cluster.head-command.yaml",
		},
		{
			name: "ray-cluster.heterogeneous.yaml",
		},
		{
			name: "ray-cluster.overwrite-command.yaml",
		},
		{
			name: "ray-cluster.py-spy.yaml",
		},
		{
			name: "ray-cluster.sample.yaml",
		},
		{
			name: "ray-cluster.separate-ingress.yaml",
		},
		{
			name: "ray-cluster.tls.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			test := With(t)
			namespace := test.NewTestNamespace()
			test.StreamKubeRayOperatorLogs()
			rayClusterFromYaml := DeserializeRayClusterSampleYAML(test, tt.name)
			KubectlApplyYAML(test, tt.name, namespace.Name)

			rayCluster := GetRayCluster(test, namespace.Name, rayClusterFromYaml.Name)
			test.Expect(rayCluster).NotTo(gomega.BeNil())

			test.T().Logf("Waiting for RayCluster %s/%s to be ready", namespace.Name, rayCluster.Name)
			test.Eventually(RayCluster(test, namespace.Name, rayCluster.Name), TestTimeoutMedium).
				Should(gomega.WithTransform(RayClusterState, gomega.Equal(rayv1.Ready)))
			rayCluster = GetRayCluster(test, namespace.Name, rayCluster.Name)

			// Check if the RayCluster created correct number of pods
			var desiredWorkerReplicas int32
			if rayCluster.Spec.WorkerGroupSpecs != nil {
				for _, workerGroupSpec := range rayCluster.Spec.WorkerGroupSpecs {
					desiredWorkerReplicas += *workerGroupSpec.Replicas
				}
			}
			test.Eventually(GetWorkerPods(test, rayCluster), TestTimeoutShort).Should(gomega.HaveLen(int(desiredWorkerReplicas)))
			test.Expect(rayCluster.Status.DesiredWorkerReplicas).To(gomega.Equal(desiredWorkerReplicas))

			// Check if the head pod is ready
			test.Eventually(GetHeadPod(test, rayCluster), TestTimeoutShort).Should(gomega.WithTransform(IsPodRunningAndReady, gomega.BeTrue()))

			// Check if all worker pods are ready
			test.Eventually(GetWorkerPods(test, rayCluster), TestTimeoutShort).Should(gomega.WithTransform(AllPodsRunningAndReady, gomega.BeTrue()))
		})
	}
}
