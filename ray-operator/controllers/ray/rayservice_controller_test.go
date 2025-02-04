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

package ray

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/ray-project/kuberay/ray-operator/controllers/ray/utils"
	"github.com/ray-project/kuberay/ray-operator/test/support"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	rayv1 "github.com/ray-project/kuberay/ray-operator/apis/ray/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	// +kubebuilder:scaffold:imports
)

func serveConfigV2Template(serveAppName string) string {
	return fmt.Sprintf(`
    applications:
    - name: %s
      import_path: fruit.deployment_graph
      route_prefix: /fruit
      runtime_env:
        working_dir: "https://github.com/ray-project/test_dag/archive/41d09119cbdf8450599f993f51318e9e27c59098.zip"
      deployments:
        - name: MangoStand
          num_replicas: 1
          user_config:
            price: 3
          ray_actor_options:
            num_cpus: 0.1
        - name: OrangeStand
          num_replicas: 1
          user_config:
            price: 2
          ray_actor_options:
            num_cpus: 0.1
        - name: PearStand
          num_replicas: 1
          user_config:
            price: 1
          ray_actor_options:
            num_cpus: 0.1`, serveAppName)
}

func rayServiceTemplate(name string, namespace string, serveAppName string) *rayv1.RayService {
	serveConfigV2 := serveConfigV2Template(serveAppName)
	return &rayv1.RayService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: rayv1.RayServiceSpec{
			ServeConfigV2: serveConfigV2,
			RayClusterSpec: rayv1.RayClusterSpec{
				RayVersion: support.GetRayVersion(),
				HeadGroupSpec: rayv1.HeadGroupSpec{
					RayStartParams: map[string]string{},
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "ray-head",
									Image: support.GetRayImage(),
								},
							},
						},
					},
				},
				WorkerGroupSpecs: []rayv1.WorkerGroupSpec{
					{
						Replicas:       ptr.To[int32](3),
						MinReplicas:    ptr.To[int32](0),
						MaxReplicas:    ptr.To[int32](10000),
						GroupName:      "small-group",
						RayStartParams: map[string]string{},
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:  "ray-worker",
										Image: support.GetRayImage(),
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func endpointsTemplate(name string, namespace string) *corev1.Endpoints {
	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP: "10.9.8.7",
					},
				},
			},
		},
	}
}

var _ = Context("RayService env tests", func() {
	Describe("Zero-downtime upgrade", Ordered, func() {
		// This test case simulates the most common scenario in the RayService code path:
		// (1) Create a RayService custom resource
		// (2) The RayService controller creates a pending RayCluster
		// (3) The serve application becomes ready on the pending RayCluster
		// (4) The Kubernetes head and serve services are created
		// (5) The pending RayCluster transitions to become the active RayCluster
		ctx := context.Background()
		namespace := "default"
		serveAppName := "app1"
		rayService := rayServiceTemplate("test-zero-downtime-path", namespace, serveAppName)
		rayCluster := &rayv1.RayCluster{}

		It("Create a RayService custom resource", func() {
			err := k8sClient.Create(ctx, rayService)
			Expect(err).NotTo(HaveOccurred(), "failed to create RayService resource")
			Eventually(
				getResourceFunc(ctx, client.ObjectKey{Name: rayService.Name, Namespace: namespace}, rayService),
				time.Second*3, time.Millisecond*500).Should(BeNil(), "RayService: %v", rayService.Name)
		})

		It("Conditions should be initialized correctly", func() {
			Eventually(
				func() bool {
					return meta.IsStatusConditionTrue(rayService.Status.Conditions, string(rayv1.UpgradeInProgress))
				},
				time.Second*3, time.Millisecond*500).Should(BeFalse(), "UpgradeInProgress condition: %v", rayService.Status.Conditions)
			Eventually(
				func() bool {
					return meta.IsStatusConditionTrue(rayService.Status.Conditions, string(rayv1.RayServiceReady))
				},
				time.Second*3, time.Millisecond*500).Should(BeFalse(), "RayServiceReady condition: %v", rayService.Status.Conditions)
		})

		It("Should create a pending RayCluster", func() {
			Eventually(
				getPreparingRayClusterNameFunc(ctx, rayService),
				time.Second*15, time.Millisecond*500).Should(Not(BeEmpty()), "Pending RayCluster name: %v", rayService.Status.PendingServiceStatus.RayClusterName)
		})

		It("Promote the pending RayCluster to the active RayCluster", func() {
			// Update the status of the head Pod to Running. Note that the default fake dashboard client
			// will return a healthy serve application status.
			pendingRayClusterName := rayService.Status.PendingServiceStatus.RayClusterName
			updateHeadPodToRunningAndReady(ctx, pendingRayClusterName, namespace)

			// Make sure the pending RayCluster becomes the active RayCluster.
			Eventually(
				getRayClusterNameFunc(ctx, rayService),
				time.Second*15, time.Millisecond*500).Should(Equal(pendingRayClusterName), "Active RayCluster name: %v", rayService.Status.ActiveServiceStatus.RayClusterName)

			// Initialize RayCluster for the following tests.
			Eventually(
				getResourceFunc(ctx, client.ObjectKey{Name: rayService.Status.ActiveServiceStatus.RayClusterName, Namespace: namespace}, rayCluster),
				time.Second*3, time.Millisecond*500).Should(BeNil(), "RayCluster: %v", rayCluster.Name)
		})

		It("Check the serve application status in the RayService status", func() {
			// Check the serve application status in the RayService status.
			// The serve application should be healthy.
			Eventually(
				checkServiceHealth(ctx, rayService),
				time.Second*3, time.Millisecond*500).Should(BeTrue(), "RayService status: %v", rayService.Status)
		})

		It("Should create a new head service resource", func() {
			svc := &corev1.Service{}
			headSvcName, err := utils.GenerateHeadServiceName(utils.RayServiceCRD, rayService.Spec.RayClusterSpec, rayService.Name)
			Expect(err).ToNot(HaveOccurred())
			Eventually(
				getResourceFunc(ctx, client.ObjectKey{Name: headSvcName, Namespace: namespace}, svc),
				time.Second*3, time.Millisecond*500).Should(BeNil(), "Head service: %v", svc)
			// TODO: Verify the head service by checking labels and annotations.
		})

		It("Should create a new serve service resource", func() {
			svc := &corev1.Service{}
			Eventually(
				getResourceFunc(ctx, client.ObjectKey{Name: utils.GenerateServeServiceName(rayService.Name), Namespace: namespace}, svc),
				time.Second*3, time.Millisecond*500).Should(BeNil(), "Serve service: %v", svc)
			// TODO: Verify the serve service by checking labels and annotations.
		})

		It("The RayServiceReady condition should be true when the number of endpoints is greater than 0", func() {
			endpoints := endpointsTemplate(utils.GenerateServeServiceName(rayService.Name), namespace)
			err := k8sClient.Create(ctx, endpoints)
			Expect(err).NotTo(HaveOccurred(), "failed to create Endpoints resource")
			Eventually(func() int32 {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: rayService.Name, Namespace: namespace}, rayService); err != nil {
					return 0
				}
				return rayService.Status.NumServeEndpoints
			}, time.Second*3, time.Millisecond*500).Should(BeNumerically(">", 0), "RayService status: %v", rayService.Status)
			Expect(meta.IsStatusConditionTrue(rayService.Status.Conditions, string(rayv1.RayServiceReady))).Should(BeTrue())
		})

		It("Should perform a zero-downtime update after rayVersion updated.", func() {
			initialClusterName, _ := getRayClusterNameFunc(ctx, rayService)()
			const mockRayVersion = "2.40.0" // Current rayVersion is 2.41.0, so set the rayVersion to 2.40.0 to test if zero-downtime is triggered.

			// The cluster shouldn't switch until deployments are finished updating
			updatingStatus := generateServeStatus(rayv1.DeploymentStatusEnum.UPDATING, rayv1.ApplicationStatusEnum.DEPLOYING)
			fakeRayDashboardClient.SetMultiApplicationStatuses(map[string]*utils.ServeApplicationStatus{serveAppName: &updatingStatus})
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				Eventually(
					getResourceFunc(ctx, client.ObjectKey{Name: rayService.Name, Namespace: "default"}, rayService),
					time.Second*3, time.Millisecond*500).Should(BeNil(), "My myRayService  = %v", rayService.Name)
				rayService.Spec.RayClusterSpec.RayVersion = mockRayVersion
				return k8sClient.Update(ctx, rayService)
			})
			Expect(err).NotTo(HaveOccurred(), "failed to update test RayService resource")

			Eventually(
				getPreparingRayClusterNameFunc(ctx, rayService),
				time.Second*60, time.Millisecond*500).Should(Not(BeEmpty()), "My new RayCluster name  = %v", rayService.Status.PendingServiceStatus.RayClusterName)

			pendingRayClusterName := rayService.Status.PendingServiceStatus.RayClusterName

			Consistently(
				getRayClusterNameFunc(ctx, rayService),
				time.Second*5, time.Millisecond*500).Should(Equal(initialClusterName), "My current RayCluster name  = %v", rayService.Status.ActiveServiceStatus.RayClusterName)

			// The cluster should switch once the deployments are finished updating
			healthyStatus := generateServeStatus(rayv1.DeploymentStatusEnum.HEALTHY, rayv1.ApplicationStatusEnum.RUNNING)
			fakeRayDashboardClient.SetMultiApplicationStatuses(map[string]*utils.ServeApplicationStatus{serveAppName: &healthyStatus})
			updateHeadPodToRunningAndReady(ctx, pendingRayClusterName, "default")

			Eventually(
				getRayClusterNameFunc(ctx, rayService),
				time.Second*60, time.Millisecond*500).Should(Equal(pendingRayClusterName), "My current RayCluster name  = %v", rayService.Status.ActiveServiceStatus.RayClusterName)
			Eventually(
				rayService.Spec.RayClusterSpec.RayVersion,
				time.Second*60, time.Millisecond*500).Should(Equal(mockRayVersion), "My current RayVersion  = %v", rayService.Spec.RayClusterSpec.RayVersion)
		})
	})

	Describe("Autoscaler updates RayCluster should not trigger zero downtime upgrade", Ordered, func() {
		// If Autoscaler scales up the pending or active RayCluster, zero downtime upgrade should not be triggered.
		ctx := context.Background()
		namespace := "default"
		serveAppName := "app1"
		rayService := rayServiceTemplate("test-autoscaler", namespace, serveAppName)
		rayCluster := &rayv1.RayCluster{}

		It("Create a RayService custom resource", func() {
			err := k8sClient.Create(ctx, rayService)
			Expect(err).NotTo(HaveOccurred(), "failed to create RayService resource")
			Eventually(
				getResourceFunc(ctx, client.ObjectKey{Name: rayService.Name, Namespace: namespace}, rayService),
				time.Second*3, time.Millisecond*500).Should(BeNil(), "RayService: %v", rayService.Name)
		})

		It("Should create a pending RayCluster", func() {
			Eventually(
				getPreparingRayClusterNameFunc(ctx, rayService),
				time.Second*15, time.Millisecond*500).Should(Not(BeEmpty()), "Pending RayCluster name: %v", rayService.Status.PendingServiceStatus.RayClusterName)
		})

		It("Autoscaler updates the pending RayCluster and should not switch to a new RayCluster", func() {
			// Simulate autoscaler by updating the pending RayCluster directly. Note that the autoscaler
			// will not update the RayService directly.
			clusterName, _ := getPreparingRayClusterNameFunc(ctx, rayService)()
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				Eventually(
					getResourceFunc(ctx, client.ObjectKey{Name: clusterName, Namespace: namespace}, rayCluster),
					time.Second*3, time.Millisecond*500).Should(BeNil(), "Pending RayCluster: %v", rayCluster.Name)
				*rayCluster.Spec.WorkerGroupSpecs[0].Replicas++
				return k8sClient.Update(ctx, rayCluster)
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to update the pending RayCluster.")

			// Confirm not switch to a new RayCluster
			Consistently(
				getPreparingRayClusterNameFunc(ctx, rayService),
				time.Second*5, time.Millisecond*500).Should(Equal(clusterName), "Pending RayCluster: %v", rayService.Status.PendingServiceStatus.RayClusterName)
		})

		It("Promote the pending RayCluster to the active RayCluster", func() {
			// Update the status of the head Pod to Running. Note that the default fake dashboard client
			// will return a healthy serve application status.
			pendingRayClusterName := rayService.Status.PendingServiceStatus.RayClusterName
			updateHeadPodToRunningAndReady(ctx, pendingRayClusterName, namespace)

			// Make sure the pending RayCluster becomes the active RayCluster.
			Eventually(
				getRayClusterNameFunc(ctx, rayService),
				time.Second*15, time.Millisecond*500).Should(Equal(pendingRayClusterName), "Active RayCluster name: %v", rayService.Status.ActiveServiceStatus.RayClusterName)

			// Initialize RayCluster for the following tests.
			Eventually(
				getResourceFunc(ctx, client.ObjectKey{Name: rayService.Status.ActiveServiceStatus.RayClusterName, Namespace: namespace}, rayCluster),
				time.Second*3, time.Millisecond*500).Should(BeNil(), "RayCluster: %v", rayCluster.Name)
		})

		It("Autoscaler updates the active RayCluster and should not switch to a new RayCluster", func() {
			// Simulate autoscaler by updating the active RayCluster directly. Note that the autoscaler
			// will not update the RayService directly.
			clusterName, _ := getRayClusterNameFunc(ctx, rayService)()
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				Eventually(
					getResourceFunc(ctx, client.ObjectKey{Name: clusterName, Namespace: namespace}, rayCluster),
					time.Second*3, time.Millisecond*500).Should(BeNil(), "Active RayCluster: %v", rayCluster.Name)
				*rayCluster.Spec.WorkerGroupSpecs[0].Replicas++
				return k8sClient.Update(ctx, rayCluster)
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to update the active RayCluster.")

			// Confirm not switch to a new RayCluster
			Consistently(
				getRayClusterNameFunc(ctx, rayService),
				time.Second*5, time.Millisecond*500).Should(Equal(clusterName), "Active RayCluster: %v", rayService.Status.ActiveServiceStatus.RayClusterName)
		})
	})

	Describe("After a RayService is running", func() {
		ctx := context.Background()
		var rayService *rayv1.RayService
		var rayCluster *rayv1.RayCluster

		BeforeEach(OncePerOrdered, func() {
			// This simulates the most common scenario in the RayService code path:
			// (1) Create a RayService custom resource
			// (2) The RayService controller creates a pending RayCluster
			// (3) The serve application becomes ready on the pending RayCluster
			// (4) The Kubernetes head and serve services are created
			// (5) The pending RayCluster transitions to become the active RayCluster
			namespace := "default"
			serveAppName := "app1"
			rayService = rayServiceTemplate("test-base-path-"+strconv.Itoa(rand.IntN(1000)), namespace, serveAppName) //nolint:gosec // no need for cryptographically secure random number
			rayCluster = &rayv1.RayCluster{}

			By("Create a RayService custom resource")
			err := k8sClient.Create(ctx, rayService)
			Expect(err).NotTo(HaveOccurred(), "failed to create RayService resource")
			Eventually(
				getResourceFunc(ctx, client.ObjectKey{Name: rayService.Name, Namespace: namespace}, rayService),
				time.Second*3, time.Millisecond*500).Should(BeNil(), "RayService: %v", rayService.Name)

			By("Conditions should be initialized correctly")
			Eventually(
				func() bool {
					return meta.IsStatusConditionTrue(rayService.Status.Conditions, string(rayv1.UpgradeInProgress))
				},
				time.Second*3, time.Millisecond*500).Should(BeFalse(), "UpgradeInProgress condition: %v", rayService.Status.Conditions)
			Eventually(
				func() bool {
					return meta.IsStatusConditionTrue(rayService.Status.Conditions, string(rayv1.RayServiceReady))
				},
				time.Second*3, time.Millisecond*500).Should(BeFalse(), "RayServiceReady condition: %v", rayService.Status.Conditions)

			By("Should create a pending RayCluster")
			Eventually(
				getPreparingRayClusterNameFunc(ctx, rayService),
				time.Second*15, time.Millisecond*500).Should(Not(BeEmpty()), "Pending RayCluster name: %v", rayService.Status.PendingServiceStatus.RayClusterName)

			By("Promote the pending RayCluster to the active RayCluster")
			// Update the status of the head Pod to Running. Note that the default fake dashboard client
			// will return a healthy serve application status.
			pendingRayClusterName := rayService.Status.PendingServiceStatus.RayClusterName
			updateHeadPodToRunningAndReady(ctx, pendingRayClusterName, namespace)

			// Make sure the pending RayCluster becomes the active RayCluster.
			Eventually(
				getRayClusterNameFunc(ctx, rayService),
				time.Second*15, time.Millisecond*500).Should(Equal(pendingRayClusterName), "Active RayCluster name: %v", rayService.Status.ActiveServiceStatus.RayClusterName)

			// Initialize RayCluster for the following tests.
			Eventually(
				getResourceFunc(ctx, client.ObjectKey{Name: rayService.Status.ActiveServiceStatus.RayClusterName, Namespace: namespace}, rayCluster),
				time.Second*3, time.Millisecond*500).Should(BeNil(), "RayCluster: %v", rayCluster.Name)

			By("Check the serve application status in the RayService status")
			// Check the serve application status in the RayService status.
			// The serve application should be healthy.
			Eventually(
				checkServiceHealth(ctx, rayService),
				time.Second*3, time.Millisecond*500).Should(BeTrue(), "RayService status: %v", rayService.Status)

			By("Should create a new head service resource")
			svc := &corev1.Service{}
			headSvcName, err := utils.GenerateHeadServiceName(utils.RayServiceCRD, rayService.Spec.RayClusterSpec, rayService.Name)
			Expect(err).ToNot(HaveOccurred())
			Eventually(
				getResourceFunc(ctx, client.ObjectKey{Name: headSvcName, Namespace: namespace}, svc),
				time.Second*3, time.Millisecond*500).Should(BeNil(), "Head service: %v", svc)
			// TODO: Verify the head service by checking labels and annotations.

			By("Should create a new serve service resource")
			svc = &corev1.Service{}
			Eventually(
				getResourceFunc(ctx, client.ObjectKey{Name: utils.GenerateServeServiceName(rayService.Name), Namespace: namespace}, svc),
				time.Second*3, time.Millisecond*500).Should(BeNil(), "Serve service: %v", svc)
			// TODO: Verify the serve service by checking labels and annotations.

			By("The RayServiceReady condition should be true when the number of endpoints is greater than 0")
			endpoints := endpointsTemplate(utils.GenerateServeServiceName(rayService.Name), namespace)
			err = k8sClient.Create(ctx, endpoints)
			Expect(err).NotTo(HaveOccurred(), "failed to create Endpoints resource")
			Eventually(func() int32 {
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: rayService.Name, Namespace: namespace}, rayService); err != nil {
					return 0
				}
				return rayService.Status.NumServeEndpoints
			}, time.Second*3, time.Millisecond*500).Should(BeNumerically(">", 0), "RayService status: %v", rayService.Status)
			Expect(meta.IsStatusConditionTrue(rayService.Status.Conditions, string(rayv1.RayServiceReady))).Should(BeTrue())
		})

		AfterEach(OncePerOrdered, func() {
			err := k8sClient.Delete(ctx, rayService)
			Expect(err).NotTo(HaveOccurred(), "failed to delete the test RayService resource")
		})

		When("updating the serveConfigV2", Ordered, func() {
			var newConfigV2 string

			BeforeAll(func() {
				newConfigV2 = serveConfigV2Template("newAppName")
				rayService.Spec.ServeConfigV2 = newConfigV2
				err := k8sClient.Update(ctx, rayService)
				Expect(err).NotTo(HaveOccurred(), "failed to update RayService resource")
			})

			It("should create an UpdatedServeApplications event", func() {
				var eventList corev1.EventList
				listOpts := []client.ListOption{
					client.InNamespace(rayService.Namespace),
					client.MatchingFields{
						"involvedObject.uid": string(rayService.UID),
						"reason":             string(utils.UpdatedServeApplications),
					},
				}
				Eventually(func() int {
					err := k8sClient.List(ctx, &eventList, listOpts...)
					Expect(err).NotTo(HaveOccurred(), "failed to list events")
					return len(eventList.Items)
				}, time.Second*15, time.Millisecond*500).Should(Equal(1))
			})

			It("refreshes rayService", func() {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: rayService.Name, Namespace: rayService.Namespace}, rayService)
				Expect(err).NotTo(HaveOccurred(), "failed to get RayService resource")
			})

			It("doesn't create a new pending cluster", func() {
				Expect(rayService.Status.PendingServiceStatus.RayClusterName).To(BeEmpty())
			})

			It("doesn't switch to a new active cluster", func() {
				Expect(rayService.Status.ActiveServiceStatus.RayClusterName).To(Equal(rayCluster.Name))
			})
		})

		When("adding a new worker group", Ordered, func() {
			BeforeAll(func() {
				newWorkerGroupSpec := rayService.Spec.RayClusterSpec.WorkerGroupSpecs[0].DeepCopy()
				newWorkerGroupSpecs := []rayv1.WorkerGroupSpec{*newWorkerGroupSpec, *newWorkerGroupSpec}
				newWorkerGroupSpecs[1].GroupName = "worker-group-to-active-cluster"

				rayService.Spec.RayClusterSpec.WorkerGroupSpecs = newWorkerGroupSpecs
				err := k8sClient.Update(ctx, rayService)
				Expect(err).NotTo(HaveOccurred(), "failed to update test RayService resource")
			})

			It("reflects the changes in the active cluster's WorkerGroupSpecs", func() {
				Eventually(
					getActiveRayClusterWorkerGroupSpecsFunc(ctx, rayService),
					time.Second*15, time.Millisecond*500).Should(HaveLen(2))
			})

			It("doesn't create a new pending cluster", func() {
				Expect(rayService.Status.PendingServiceStatus.RayClusterName).To(BeEmpty())
			})

			It("doesn't switch to a new active cluster", func() {
				Expect(rayService.Status.ActiveServiceStatus.RayClusterName).To(Equal(rayCluster.Name))
			})
		})

		When("during the zero-downtime upgrade", func() {
			var pendingClusterName string

			BeforeEach(OncePerOrdered, func() {
				rayService.Spec.RayClusterSpec.RayVersion += "-next"
				err := k8sClient.Update(ctx, rayService)
				Expect(err).NotTo(HaveOccurred(), "failed to update RayService resource")

				Eventually(
					getPreparingRayClusterNameFunc(ctx, rayService),
					time.Second*15, time.Millisecond*500).Should(Not(BeEmpty()), "Pending RayCluster name: %v", rayService.Status.PendingServiceStatus.RayClusterName)
				pendingClusterName = rayService.Status.PendingServiceStatus.RayClusterName
			})

			When("updating the RayVersion again", Ordered, func() {
				BeforeAll(func() {
					rayService.Spec.RayClusterSpec.RayVersion += "-next2"
					err := k8sClient.Update(ctx, rayService)
					Expect(err).NotTo(HaveOccurred(), "failed to update RayService resource")
				})

				It("doesn't create a new pending cluster", func() {
					Consistently(
						getPreparingRayClusterNameFunc(ctx, rayService),
						time.Second*10, time.Millisecond*500).Should(Equal(pendingClusterName), "Pending RayCluster name  = %v", rayService.Status.PendingServiceStatus.RayClusterName)
				})

				It("doesn't switch to a new active cluster", func() {
					Expect(rayService.Status.ActiveServiceStatus.RayClusterName).To(Equal(rayCluster.Name))
				})
			})

			When("adding a new worker group", Ordered, func() {
				BeforeAll(func() {
					newWorkerGroupSpec := rayService.Spec.RayClusterSpec.WorkerGroupSpecs[0].DeepCopy()
					newWorkerGroupSpecs := []rayv1.WorkerGroupSpec{*newWorkerGroupSpec, *newWorkerGroupSpec}
					newWorkerGroupSpecs[1].GroupName = "worker-group-to-pending-cluster"

					rayService.Spec.RayClusterSpec.WorkerGroupSpecs = newWorkerGroupSpecs
					err := k8sClient.Update(ctx, rayService)
					Expect(err).NotTo(HaveOccurred(), "failed to update test RayService resource")
				})

				It("reflects the changes in the pending cluster's WorkerGroupSpecs", func() {
					Eventually(
						getPendingRayClusterWorkerGroupSpecsFunc(ctx, rayService),
						time.Second*15, time.Millisecond*500).Should(HaveLen(2))
				})

				It("doesn't switch to a new active cluster", func() {
					Expect(rayService.Status.ActiveServiceStatus.RayClusterName).To(Equal(rayCluster.Name))
				})
			})
		})
	})
})
