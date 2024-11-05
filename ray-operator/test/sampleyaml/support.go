package sampleyaml

import (
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	rayv1 "github.com/ray-project/kuberay/ray-operator/apis/ray/v1"
	. "github.com/ray-project/kuberay/ray-operator/test/support"
)

func GetSampleYAMLDir(t Test) string {
	t.T().Helper()
	_, b, _, _ := runtime.Caller(0)
	sampleYAMLDir := filepath.Join(filepath.Dir(b), "../../config/samples")
	info, err := os.Stat(sampleYAMLDir)
	assert.NoError(t.T(), err)
	assert.True(t.T(), info.IsDir())
	return sampleYAMLDir
}

func IsPodRunningAndReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func AllPodsRunningAndReady(pods []corev1.Pod) bool {
	for _, pod := range pods {
		if !IsPodRunningAndReady(&pod) {
			return false
		}
	}
	return true
}

func SubmitJobsToAllPods(t Test, rayCluster *rayv1.RayCluster) func(Gomega) {
	return func(g Gomega) {
		pods, err := GetAllPods(t, rayCluster)
		g.Expect(err).NotTo(HaveOccurred())
		cmd := []string{
			"python",
			"-c",
			"import ray; ray.init(); print(ray.cluster_resources())",
		}
		for _, pod := range pods {
			for _, container := range pod.Spec.Containers {
				ExecPodCmd(t, &pod, container.Name, cmd)
			}
		}
	}
}
