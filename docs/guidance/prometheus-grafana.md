# Ray Cluster: Monitoring with Prometheus & Grafana

This section will describe how to monitor Ray Clusters in Kubernetes using Prometheus & Grafana.

If you do not have any experience with Prometheus and Grafana on Kubernetes, I strongly recommend you watch this [YouTube playlist](https://youtube.com/playlist?list=PLy7NrYWoggjxCF3av5JKwyG7FFF9eLeL4).

## Step 1: Create a Kubernetes cluster with Kind.

```sh
kind create cluster
```

## Step 2: Install Kubernetes Prometheus Stack via Helm chart

```sh
# Path: kuberay/
./install/prometheus/install.sh

# Check the installation
kubectl get all -n prometheus-system

# (part of the output)
# NAME                                                  READY   UP-TO-DATE   AVAILABLE   AGE
# deployment.apps/prometheus-grafana                    1/1     1            1           46s
# deployment.apps/prometheus-kube-prometheus-operator   1/1     1            1           46s
# deployment.apps/prometheus-kube-state-metrics         1/1     1            1           46s
```
* KubeRay provides an [install.sh script](../../install/prometheus/install.sh) to install the [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack) chart and related custom resources, including **ServiceMonitor** and **PodMonitor**, in the namespace `prometheus-system` automatically.

## Step 3: Install a KubeRay operator

* Follow this [document](../../helm-chart/kuberay-operator/README.md) to install the latest stable KubeRay operator via Helm repository.

## Step 4: Install a RayCluster

```sh
helm install raycluster kuberay/ray-cluster --version 0.4.0

# Check ${RAYCLUSTER_HEAD_POD}
kubectl get pod -l ray.io/node-type=head

# Example output:
# NAME                            READY   STATUS    RESTARTS   AGE
# raycluster-kuberay-head-btwc2   1/1     Running   0          63s

# Wait until all Ray Pods are running and forward the port of the Prometheus metrics endpoint in a new terminal.
kubectl port-forward --address 0.0.0.0 ${RAYCLUSTER_HEAD_POD} 8080:8080
curl localhost:8080

# Example output (Prometheus metrics format):
# # HELP ray_spill_manager_request_total Number of {spill, restore} requests.
# # TYPE ray_spill_manager_request_total gauge
# ray_spill_manager_request_total{Component="raylet",NodeAddress="10.244.0.13",Type="Restored",Version="2.0.0"} 0.0

# Ensure that the port (8080) for the metrics endpoint is also defined in the head's Kubernetes service.
kubectl get service

# NAME                          TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)                                         AGE
# raycluster-kuberay-head-svc   ClusterIP   10.96.201.142   <none>        6379/TCP,8265/TCP,8080/TCP,8000/TCP,10001/TCP   106m
```

* Based on [kuberay/#230](https://github.com/ray-project/kuberay/pull/230), KubeRay will expose a Prometheus metrics endpoint in port **8080** via a built-in exporter by default. Hence, we do not need to install any external exporter.
* If you want to configure the metrics endpoint to a different port, also see [kuberay/#230](https://github.com/ray-project/kuberay/pull/230) for more details.
* Prometheus metrics format:
  * `# HELP`: Describe the meaning of this metric.
  * `# TYPE`: See [this document](https://prometheus.io/docs/concepts/metric_types/) for more details.

## Step 5: Collect Head Node metrics with a ServiceMonitor

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: ray-head-monitor
  namespace: prometheus-system
  labels:
    # `release: $HELM_RELEASE`: Prometheus can only detect ServiceMonitor with this label.
    release: prometheus
spec:
  jobLabel: ray-head
  # Only select Kubernetes Services in the "default" namespace.
  namespaceSelector:
    matchNames:
      - default
  # Only select Kubernetes Services with "matchLabels".
  selector:
    matchLabels:
      ray.io/node-type: head
  # A list of endpoints allowed as part of this ServiceMonitor.
  endpoints:
    - port: metrics
  targetLabels:
  - ray.io/cluster
```
* The YAML example above is [serviceMonitor.yaml](../../config/prometheus/serviceMonitor.yaml), and it is created by **install.sh**. Hence, no need to create anything here.
* See [ServiceMonitor official document](https://github.com/prometheus-operator/prometheus-operator/blob/main/Documentation/api.md#servicemonitor) for more details about the configurations.

* `release: $HELM_RELEASE`: Prometheus can only detect ServiceMonitor with this label.
  ```sh
  helm ls -n prometheus-system
  # ($HELM_RELEASE is "prometheus".)
  # NAME            NAMESPACE               REVISION        UPDATED                                 STATUS          CHART                           APP VERSION
  # prometheus      prometheus-system       1               2023-02-06 06:27:05.530950815 +0000 UTC deployed        kube-prometheus-stack-44.3.1    v0.62.0

  kubectl get prometheuses.monitoring.coreos.com -n prometheus-system -oyaml
  # podMonitorSelector:
  #   matchLabels:
  #     release: prometheus
  # ...
  # serviceMonitorSelector:
  #   matchLabels:
  #     release: prometheus
  ```

* `namespaceSelector` and `seletor` are used to select exporter's Kubernetes service. Because Ray uses a built-in exporter, the **ServiceMonitor** selects Ray's head service which exposes the metrics endpoint (i.e. port 8080 here).
  ```sh
  kubectl get service -n default -l ray.io/node-type=head
  # NAME                          TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)                                         AGE
  # raycluster-kuberay-head-svc   ClusterIP   10.96.201.142   <none>        6379/TCP,8265/TCP,8080/TCP,8000/TCP,10001/TCP   153m
  ```

* `targetLabels`: We added `spec.targetLabels[0].ray.io/cluster` because we want to include the name of the RayCluster in the metrics that will be generated by this ServiceMonitor. The `ray.io/cluster` label is part of the Ray head node service and it will be transformed into a `ray_io_cluster` metric label. That is, any metric that will be imported, will also contain the following label `ray_io_cluster=<ray-cluster-name>`. This may seem optional but it becomes mandatory if you deploy multiple RayClusters.

## Step 6: Collect Worker Node metrics with PodMonitors

KubeRay operator does not create a Kubernetes service for the Ray worker Pods, therefore we cannot use a Prometheus ServiceMonitor to scrape the metrics from the worker Pods. To collect worker metrics, we can use `Prometheus PodMonitors CRD` instead.

**Note**: We could create a Kubernetes service with selectors a common label subset from our worker pods, however, this is not ideal because our workers are independent from each other, that is, they are not a collection of replicas spawned by replicaset controller. Due to that, we should avoid using a Kubernetes service for grouping them together.

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: ray-workers-monitor
  namespace: prometheus-system
  labels:
    # `release: $HELM_RELEASE`: Prometheus can only detect PodMonitor with this label.
    release: prometheus
    ray.io/cluster: raycluster-kuberay # $RAY_CLUSTER_NAME: "kubectl get rayclusters.ray.io"
spec:
  jobLabel: ray-workers
  # Only select Kubernetes Pods in the "default" namespace.
  namespaceSelector:
    matchNames:
      - default
  # Only select Kubernetes Pods with "matchLabels".
  selector:
    matchLabels:
      ray.io/node-type: worker
  # A list of endpoints allowed as part of this PodMonitor.
  podMetricsEndpoints:
  - port: metrics
```
* **PodMonitor** in `namespaceSelector` and `selector` are used to select Kubernetes Pods.
  ```sh
  kubectl get pod -n default -l ray.io/node-type=worker
  # NAME                                          READY   STATUS    RESTARTS   AGE
  # raycluster-kuberay-worker-workergroup-5stpm   1/1     Running   0          3h16m
  ```

* `ray.io/cluster: $RAY_CLUSTER_NAME`: We also define `metadata.labels` by manually adding `ray.io/cluster: <ray-cluster-name>` and then instructing the PodMonitors resource to add that label in the scraped metrics via `spec.podTargetLabels[0].ray.io/cluster`.

## Step 7: Access Prometheus Web UI
```sh
# Forward the port of Prometheus Web UI in the Prometheus server Pod.
kubectl port-forward --address 0.0.0.0 prometheus-prometheus-kube-prometheus-prometheus-0 -n prometheus-system 9090:9090

# Check ${YOUR_IP}:9090/targets for the Web UI (e.g. 127.0.0.1:9090/targets)
# You should be able to see "podMonitor/prometheus-system/ray-workers-monitor/0 (1/1 up)"
# and "serviceMonitor/prometheus-system/ray-head-monitor/0 (1/1 up)" in the page.
```

![Prometheus Web UI](../images/prometheus_web_ui.png)

## Step 8: Access Grafana

```sh
# Forward the port of Grafana
kubectl port-forward --address 0.0.0.0 deployment/prometheus-grafana -n prometheus-system 3000:3000

# Check ${YOUR_IP}:3000 for the Grafana login page (e.g. 127.0.0.1:3000).
# The default username is "admin" and the password is "prom-operator".
```

* The default password is defined by `grafana.adminPassword` in the [values.yaml](https://github.com/prometheus-community/helm-charts/blob/main/charts/kube-prometheus-stack/values.yaml) of the kube-prometheus-stack chart.

* After logging in to Grafana successfully, we can import Ray Dashboard into Grafana via **dashboard_default.json**.
  * Click "Dashboards" icon in the left panel.
  * Click "Import".
  * Click "Upload JSON file".
  * Choose [config/grafana/dashboard_default.json](../../config/grafana/dashboard_default.json).
  * Click "Import".

![Grafana Ray Dashboard](../images/grafana_ray_dashboard.png)

### Custom Metrics & Alerting 

We can also define custom metrics, and create alerts by using `prometheusrules.monitoring.coreos.com` CRD. Because custom metrics, and alerting is different for each team and setup, we have included an example under [config/prometheus/rules](../../config/prometheus/rules/) that you can use to build custom metrics and alerts.
