## Ray Services (alpha)

> Note: This is the alpha version of Ray Services. There will be ongoing improvements for Ray Services in the future releases.

### Prerequisite

* Ray 2.0 is required.

### What is a RayService?

The RayService is a new custom resource (CR) supported by KubeRay in v0.3.0.

A RayService manages 2 things:
* RayCluster: Manages resources in kubernetes cluster.
* Ray Serve Deployment Graph: Manages users' serve deployment graph.

### What does the RayService provide?

* Kubernetes-native support for Ray cluster and Ray Serve deployment graphs. You can use a kubernetes config to define a ray cluster and its ray serve deployment graphs. Then you can use `kubectl` to create the cluster and its graphs.
* In-place update for ray serve deployment graph. Users can update the ray serve deployment graph config in the RayService CR config and use `kubectl apply` to update the serve deployment graph.
* Zero downtime upgrade for ray cluster. Users can update the ray cluster config in the RayService CR config and use `kubectl apply` to update the ray cluster. RayService will temporarily create a pending ray cluster, wait for the pending ray cluster ready, and then switch traffics to the new ray cluster, terminate the old cluster. 
* Services HA. RayService will monitor the ray cluster and serve deployments health status. If RayService detects any unhealthy status lasting for a certain time, RayService will try to create a new ray cluster, and switch traffic to the new cluster when it is ready.

### Deploy the Operator

`$ kubectl apply -k "github.com/ray-project/kuberay/ray-operator/config/default"`

Check that the controller is running.

```shell
$ kubectl get deployments -n ray-system
NAME           READY   UP-TO-DATE   AVAILABLE   AGE
ray-operator   1/1     1            1           40s

$ kubectl get pods -n ray-system
NAME                            READY   STATUS    RESTARTS   AGE
ray-operator-75dbbf8587-5lrvn   1/1     Running   0          31s
```

### Run an example cluster

There is one example config file to deploy RaySerive included here:
[ray_v1alpha1_rayservice.yaml](https://github.com/ray-project/kuberay/blob/master/ray-operator/config/samples/ray_v1alpha1_rayservice.yaml)

```shell
# Create a ray service and deploy fruit deployment graph.
$ kubectl apply -f config/samples/ray_v1alpha1_rayservice.yaml
```

```shell
# List running RayServices.
$ kubectl get rayservice
NAME                AGE
rayservice-sample   7s
```

```shell
# The created RayService should include a head pod, a worker pod, and four services.
$ kubectl get pods
NAME                                                      READY   STATUS    RESTARTS   AGE
ervice-sample-raycluster-qd2vl-worker-small-group-bxpp6   1/1     Running   0          24m
rayservice-sample-raycluster-qd2vl-head-45hj4             1/1     Running   0          24m

$ kubectl get services
NAME                                               TYPE        CLUSTER-IP       EXTERNAL-IP   PORT(S)                                          AGE
kubernetes                                         ClusterIP   10.100.0.1       <none>        443/TCP                                          62d
# A head node service maintained by the RayService.
rayservice-sample-head-svc                         ClusterIP   10.100.34.24     <none>        6379/TCP,8265/TCP,10001/TCP,8000/TCP,52365/TCP   24m
# A dashboard agent service maintained by the RayCluster.
rayservice-sample-raycluster-qd2vl-dashboard-svc   ClusterIP   10.100.109.177   <none>        52365/TCP                                        24m
# A head node service maintained by the RayCluster.
rayservice-sample-raycluster-qd2vl-head-svc        ClusterIP   10.100.180.221   <none>        6379/TCP,8265/TCP,10001/TCP,8000/TCP,52365/TCP   24m
# A serve service maintained by the RayService.
rayservice-sample-serve-svc                        ClusterIP   10.100.39.92     <none>        8000/TCP                                         24m
```

> Note: Default ports and their definition. 

| Port  | Definition          |
|-------|---------------------|
| 6379  | Ray GCS             |
| 8265  | Ray Dashboard       |
| 10001 | Ray Client          |
| 8000  | Ray Serve           |
| 52365 | Ray Dashboard Agent |

Get the RayService information with your RayService name.
```shell
$ kubectl describe  rayservices rayservice-sample
```

### Access User Services

The users' traffic can go through the `serve` service (for example, `rayservice-sample-serve-svc`).

#### Run a curl pod
`kubectl run curl --image=radial/busyboxplus:curl -i --tty` 
Or if you already have a curl pod running, you can login with `kubectl exec -it curl sh`.

For the fruit example deployment, you can try the following request
```shell
[ root@curl:/ ]$ curl  -X POST -H 'Content-Type: application/json' rayservice-sample-serve-svc.default.svc.cluster.local:8000 -d '["MANGO", 2]'
6
```
You can get the response as `6`.

#### Use Port Forwarding
Set up kubernetes port forwarding.
```shell
$ kubectl port-forward service/rayservice-sample-serve-svc 8000
```
For the fruit example deployment, you can try the following request
```shell
curl  -X POST -H 'Content-Type: application/json' localhost:8000 -d '["MANGO", 2]'
6
```

`serve-svc` is HA in general.
* Note: serve-svc will do traffic routing among all the workers which have serve deployments.
* Note: serve-svc will always try it best to point to the healthy cluster, even during upgrading or failing cases.
* Note: You can set `serviceUnhealthySecondThreshold` to define the threshold of seconds that the serve deployments fail.
* Note: You can set `deploymentUnhealthySecondThreshold` to define the threshold of seconds that the Ray fails to deploy any serve deployments.

### Access Ray Dashboard
Set up kubernetes port forwarding for the dashboard.
```shell
$ kubectl port-forward service/rayservice-sample-head-svc 8265
```
Then you can open your web browser with the url localhost:8265 to see your Ray dashboard page.

### Update Ray Serve Deployment Graph

You can update the `serveDeploymentGraphConfig` in your RayService config file.
For example, if you update the mango price to 4 in [ray_v1alpha1_rayservice.yaml](https://github.com/ray-project/kuberay/blob/master/ray-operator/config/samples/ray_v1alpha1_rayservice.yaml).
```shell
  - name: MangoStand
    numReplicas: 1
    userConfig: |
      price: 4
```

Do a `kubectl apply` to update your RayService.

You can check the kubernetes stats of your RayService. It should show similar:
```shell
    serveDeploymentStatuses:
    - healthLastUpdateTime: "2022-07-18T21:51:37Z"
      lastUpdateTime: "2022-07-18T21:51:41Z"
      name: MangoStand
      status: UPDATING
```

After it finishes deployment, let's send a request again.
```shell
# In the curl pod.
[ root@curl:/ ]$ curl  -X POST -H 'Content-Type: application/json' rayservice-sample-serve-svc.default.svc.cluster.local:8000 -d '["MANGO", 2]'
8
```
Or
```shell
# Using port forwarding.
curl  -X POST -H 'Content-Type: application/json' localhost:8000 -d '["MANGO", 2]'
8
```
Now you will get `8` as a result.

### Upgrade RayService RayCluster Config
You can update the `rayClusterConfig` in your RayService config file.
For example, you can increase the worker node num to 2.
```shell
workerGroupSpecs:
  # the pod replicas in this group typed worker
  - replicas: 2
```

Do a `kubectl apply` to update your RayService.

You can check the kubernetes stats of your RayService. It should show similar:
```shell
  pendingServiceStatus:
    appStatus: {}
    dashboardStatus:
      healthLastUpdateTime: "2022-07-18T21:54:53Z"
      lastUpdateTime: "2022-07-18T21:54:54Z"
    rayClusterName: rayservice-sample-raycluster-bshfr
    rayClusterStatus: {}
```
You can see RayService is preparing a pending cluster. After the pending cluster is healthy, RayService will switch it as active cluster and terminate the previous cluster.

### RayService Observability
You can use `kubectl logs` to check the operator logs or the head/worker nodes logs.
You can also use `kubectl describe rayservices rayservice-sample` to check the states and event logs of your RayService instance.

You can also login the head pod and use Ray cli to check the logs.
`kubectl exec -it <head-node-pod> bash`

### Delete the RayService instance
`$ kubectl delete -f config/samples/ray_v1alpha1_rayservice.yaml`

### Delete the operator

`$ kubectl delete -k "github.com/ray-project/kuberay/ray-operator/config/default"`