# RayService troubleshooting

RayService is a Custom Resource Definition (CRD) designed for Ray Serve. In KubeRay, creating a RayService will first create a RayCluster and then
create Ray Serve applications once the RayCluster is ready. If the issue pertains to the data plane, specifically your Ray Serve scripts 
or Ray Serve configurations (`serveConfigV2`), troubleshooting may be challenging. This section provides some tips to help you debug these issues.

## Observability

### Method 1: Check KubeRay operator's logs for errors

```bash
kubectl logs $KUBERAY_OPERATOR_POD -n $YOUR_NAMESPACE | tee operator-log
```

The above command will redirect the operator's logs to a file called `operator-log`. You can then search for errors in the file.

### Method 2: Check RayService CR status

```bash
kubectl describe rayservice $RAYSERVICE_NAME -n $YOUR_NAMESPACE
```

You can check the status and events of the RayService CR to see if there are any errors.

### Method 3: Check logs of Ray Pods

You can also check the Ray Serve logs directly by accessing the log files on the pods. These log files contain system level logs from the Serve controller and HTTP proxy as well as access logs and user-level logs. See [Ray Serve Logging](https://docs.ray.io/en/latest/serve/production-guide/monitoring.html#ray-logging) and [Ray Logging](https://docs.ray.io/en/latest/ray-observability/user-guides/configure-logging.html#configure-logging) for more details.
```bash
kubectl exec -it $RAY_POD -n $YOUR_NAMESPACE -- bash
# Check the logs under /tmp/ray/session_latest/logs/serve/
```

### Method 4: Check Dashboard

```bash
kubectl port-forward $RAY_POD -n $YOUR_NAMESPACE --address 0.0.0.0 8265:8265
# Check $YOUR_IP:8265 in your browser
```

For more details about Ray Serve observability on the dashboard, you can refer to [the documentation](https://docs.ray.io/en/latest/ray-observability/getting-started.html#serve-view) and [the YouTube video](https://youtu.be/eqXfwM641a4).

## Common issues

### Issue 1: Ray Serve script is incorrect.

We strongly recommend that you test your Ray Serve script locally or in a RayCluster before
deploying it to a RayService. [TODO: https://github.com/ray-project/kuberay/issues/1176]

### Issue 2: `serveConfigV2` is incorrect.

For the sake of flexibility, we have set `serveConfigV2` as a YAML multi-line string in the RayService CR.
This implies that there is no strict type checking for the Ray Serve configurations in `serveConfigV2` field.
Some tips to help you debug the `serveConfigV2` field:

* Check [the documentation](https://docs.ray.io/en/latest/serve/api/#put-api-serve-applications) for the schema about
the Ray Serve Multi-application API `PUT "/api/serve/applications/"`.
* Unlike `serveConfig`, `serveConfigV2` adheres to the snake case naming convention. For example, `numReplicas` is used in `serveConfig`, while `num_replicas` is used in `serveConfigV2`. 

### Issue 3-1: The Ray image does not include the required dependencies.

You have two options to resolve this issue:

* Build your own Ray image with the required dependencies.
* Specify the required dependencies via `runtime_env` in `serveConfigV2` field.
  * For example, the MobileNet example requires `python-multipart`, which is not included in the Ray image `rayproject/ray-ml:2.5.0`.
Therefore, the YAML file includes `python-multipart` in the runtime environment. For more details, refer to [the MobileNet example](mobilenet-rayservice.md).

### Issue 3-2: Examples for troubleshooting dependency issues.

> Note: We highly recommend testing your Ray Serve script locally or in a RayCluster before deploying it to a RayService. This helps identify any dependency issues in the early stages. [TODO: https://github.com/ray-project/kuberay/issues/1176]

In the [MobileNet example](mobilenet-rayservice.md), the [mobilenet.py](https://github.com/ray-project/serve_config_examples/blob/master/mobilenet/mobilenet.py) consists of two functions: `__init__()` and `__call__()`.
The function `__call__()` will only be called when the Serve application receives a request.

* Example 1: Remove `python-multipart` from the runtime environment in [the MobileNet YAML](../../ray-operator/config/samples/ray-service.mobilenet.yaml).
  * The `python-multipart` library is only required for the `__call__` method. Therefore, we can only observe the dependency issue when we send a request to the application.
  * Example error message:
    ```bash
    Unexpected error, traceback: ray::ServeReplica:mobilenet_ImageClassifier.handle_request() (pid=226, ip=10.244.0.9)
      .
      .
      .
      File "...", line 24, in __call__
        request = await http_request.form()
      File "/home/ray/anaconda3/lib/python3.7/site-packages/starlette/requests.py", line 256, in _get_form
        ), "The `python-multipart` library must be installed to use form parsing."
    AssertionError: The `python-multipart` library must be installed to use form parsing..
    ```

* Example 2: Update the image from `rayproject/ray-ml:2.5.0` to `rayproject/ray:2.5.0` in [the MobileNet YAML](../../ray-operator/config/samples/ray-service.mobilenet.yaml). The latter image does not include `tensorflow`.
  * The `tensorflow` library is imported in the [mobilenet.py](https://github.com/ray-project/serve_config_examples/blob/master/mobilenet/mobilenet.py).
  * Example error message:
    ```bash
    kubectl describe rayservices.ray.io rayservice-mobilenet

    # Example error message:
    Pending Service Status:
      Application Statuses:
        Mobilenet:
          ...
          Message:                  Deploying app 'mobilenet' failed:
            ray::deploy_serve_application() (pid=279, ip=10.244.0.12)
                ...
              File ".../mobilenet/mobilenet.py", line 4, in <module>
                from tensorflow.keras.preprocessing import image
            ModuleNotFoundError: No module named 'tensorflow'
    ```

### Issue 4: Incorrect `import_path`.

You can refer to [the documentation](https://docs.ray.io/en/latest/serve/api/doc/ray.serve.schema.ServeApplicationSchema.html#ray.serve.schema.ServeApplicationSchema.import_path) for more details about the format of `import_path`.
Taking [the MobileNet YAML file](../../ray-operator/config/samples/ray-service.mobilenet.yaml) as an example,
the `import_path` is `mobilenet.mobilenet:app`. The first `mobilenet` is the name of the directory in the `working_dir`,
the second `mobilenet` is the name of the Python file in the directory `mobilenet/`,
and `app` is the name of the variable representing Ray Serve application within the Python file.

```yaml
  serveConfigV2: |
    applications:
      - name: mobilenet
        import_path: mobilenet.mobilenet:app
        runtime_env:
          working_dir: "https://github.com/ray-project/serve_config_examples/archive/b393e77bbd6aba0881e3d94c05f968f05a387b96.zip"
          pip: ["python-multipart==0.0.6"]
```

### Issue 5: Fail to create / update Serve applications.

You may encounter the following error message when KubeRay tries to create / update Serve applications:

```
Put "http://${HEAD_SVC_FQDN}:52365/api/serve/applications/": dial tcp $HEAD_IP:52365: connect: connection refused
```

For RayService, the KubeRay operator submits a request to the RayCluster for creating Serve applications once the head Pod is ready.
It's important to note that the Dashboard and GCS may take a few seconds to start up after the head Pod is ready.
As a result, the request may fail a few times initially before the necessary components are fully operational.

If you continue to encounter this issue after 1 minute, there are several possible causes:

* The Dashboard and dashboard agent failed to start up due to some reasons. You can check the `dashboard.log` and `dashboard_agent.log` files located at `/tmp/ray/session_latest/logs/` on the head Pod for more information.

* There is a Kubernetes NetworkPolicy blocking the traffic between the KubeRay operator and the dashboard agent port (i.e., 52365). Please review your NetworkPolicy configuration.

### Issue 6: `runtime_env`

In `serveConfigV2`, you can specify the runtime environment for the Ray Serve applications via `runtime_env`.
Some common issues related to `runtime_env`:

* The `working_dir` points to a private AWS S3 bucket, but the Ray Pods do not have the necessary permissions to access the bucket.

* The NetworkPolicy blocks the traffic between the Ray Pods and the external URLs specified in `runtime_env`.
