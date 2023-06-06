package utils

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	fmtErrors "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/util/yaml"

	"k8s.io/apimachinery/pkg/util/json"

	rayv1alpha1 "github.com/ray-project/kuberay/ray-operator/apis/ray/v1alpha1"
)

// TODO: currently the following constants are also declared in ray-operator/controllers/ray/common
// We cannot import them to avoid cycles
const (
	DefaultDashboardName                = "dashboard"
	DefaultDashboardAgentListenPortName = "dashboard-agent"
)

var (
	DeployPath       = "/api/serve/deployments/"
	StatusPath       = "/api/serve/deployments/status"
	ServeDetailsPath = "/api/serve/applications/"
	JobPath          = "/api/jobs/"
)

// ServingClusterDeployments defines the request sent to the dashboard api server.
// See https://docs.ray.io/en/master/_modules/ray/serve/schema.html#ServeApplicationSchema for more details.
type ServingClusterDeployments struct {
	ImportPath  string                 `json:"import_path"`
	RuntimeEnv  map[string]interface{} `json:"runtime_env,omitempty"`
	Deployments []ServeConfigSpec      `json:"deployments,omitempty"`
	Port        int                    `json:"port,omitempty"`
}

type RayDashboardClientInterface interface {
	InitClient(url string)
	GetDeployments(context.Context) (string, error)
	UpdateDeployments(ctx context.Context, spec rayv1alpha1.ServeDeploymentGraphSpec) error
	// V1/single-app Rest API
	GetSingleApplicationStatus(context.Context) (*ServeApplicationStatus, error)
	// V2/multi-app Rest API
	GetServeDetails(ctx context.Context) (*ServeDetails, error)
	GetMultiApplicationStatus(context.Context) (map[string]*ServeApplicationStatus, error)
	ConvertServeConfig(specs []rayv1alpha1.ServeConfigSpec) []ServeConfigSpec
	GetJobInfo(ctx context.Context, jobId string) (*RayJobInfo, error)
	SubmitJob(ctx context.Context, rayJob *rayv1alpha1.RayJob, log *logr.Logger) (jobId string, err error)
	StopJob(ctx context.Context, jobName string, log *logr.Logger) (err error)
}

// GetRayDashboardClientFunc Used for unit tests.
var GetRayDashboardClientFunc = GetRayDashboardClient

func GetRayDashboardClient() RayDashboardClientInterface {
	return &RayDashboardClient{}
}

type RayDashboardClient struct {
	client       http.Client
	dashboardURL string
}

func FetchDashboardAgentURL(ctx context.Context, log *logr.Logger, cli client.Client, rayCluster *rayv1alpha1.RayCluster) (string, error) {
	dashboardAgentService := &corev1.Service{}
	dashboardAgentServiceName := CheckName(GenerateDashboardServiceName(rayCluster.Name))
	if err := cli.Get(ctx, client.ObjectKey{Name: dashboardAgentServiceName, Namespace: rayCluster.Namespace}, dashboardAgentService); err != nil {
		return "", err
	}

	log.V(1).Info("fetchDashboardAgentURL ", "dashboard agent service found", dashboardAgentService.Name)
	// TODO: compare diff and reconcile the object. For example. ServiceType might be changed or port might be modified
	servicePorts := dashboardAgentService.Spec.Ports

	dashboardPort := int32(-1)

	for _, servicePort := range servicePorts {
		if servicePort.Name == DefaultDashboardAgentListenPortName {
			dashboardPort = servicePort.Port
			break
		}
	}

	if dashboardPort == int32(-1) {
		return "", fmtErrors.Errorf("dashboard port not found")
	}

	domainName := GetClusterDomainName()
	dashboardAgentURL := fmt.Sprintf("%s.%s.svc.%s:%v",
		dashboardAgentService.Name,
		dashboardAgentService.Namespace,
		domainName,
		dashboardPort)
	log.V(1).Info("fetchDashboardAgentURL ", "dashboardURL", dashboardAgentURL)
	return dashboardAgentURL, nil
}

func FetchDashboardURL(ctx context.Context, log *logr.Logger, cli client.Client, rayCluster *rayv1alpha1.RayCluster) (string, error) {
	headSvc := &corev1.Service{}
	headSvcName := GenerateServiceName(rayCluster.Name)
	if err := cli.Get(ctx, client.ObjectKey{Name: headSvcName, Namespace: rayCluster.Namespace}, headSvc); err != nil {
		return "", err
	}

	log.V(3).Info("fetchDashboardURL ", "dashboard service found", headSvc.Name)
	servicePorts := headSvc.Spec.Ports
	dashboardPort := int32(-1)

	for _, servicePort := range servicePorts {
		if servicePort.Name == DefaultDashboardName {
			dashboardPort = servicePort.Port
			break
		}
	}

	if dashboardPort == int32(-1) {
		return "", fmtErrors.Errorf("dashboard port not found")
	}

	domainName := GetClusterDomainName()
	dashboardURL := fmt.Sprintf("%s.%s.svc.%s:%v",
		headSvc.Name,
		headSvc.Namespace,
		domainName,
		dashboardPort)
	log.V(1).Info("fetchDashboardURL ", "dashboardURL", dashboardURL)
	return dashboardURL, nil
}

func (r *RayDashboardClient) InitClient(url string) {
	r.client = http.Client{
		Timeout: 120 * time.Second,
	}
	r.dashboardURL = "http://" + url
}

// GetDeployments get the current deployments in the Ray cluster.
func (r *RayDashboardClient) GetDeployments(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", r.dashboardURL+DeployPath, nil)
	if err != nil {
		return "", err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("GetDeployments fail: %s %s", resp.Status, string(body))
	}

	return string(body), nil
}

// UpdateDeployments update the deployments in the Ray cluster.
func (r *RayDashboardClient) UpdateDeployments(ctx context.Context, spec rayv1alpha1.ServeDeploymentGraphSpec) error {
	runtimeEnv := make(map[string]interface{})
	_ = yaml.Unmarshal([]byte(spec.RuntimeEnv), &runtimeEnv)

	servingClusterDeployments := ServingClusterDeployments{
		ImportPath:  spec.ImportPath,
		RuntimeEnv:  runtimeEnv,
		Deployments: r.ConvertServeConfig(spec.ServeConfigSpecs),
		Port:        spec.Port,
	}

	deploymentJson, err := json.Marshal(servingClusterDeployments)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, r.dashboardURL+DeployPath, bytes.NewBuffer(deploymentJson))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("UpdateDeployments fail: %s %s", resp.Status, string(body))
	}

	return nil
}

// GetDeploymentsStatus get the current deployment statuses in the Ray cluster.
func (r *RayDashboardClient) GetSingleApplicationStatus(ctx context.Context) (*ServeApplicationStatus, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", r.dashboardURL+StatusPath, nil)
	if err != nil {
		return nil, err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("GetDeploymentsStatus fail: %s %s", resp.Status, string(body))
	}

	var status ServeSingleApplicationStatusV1
	if err = json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("Failed to unmarshal bytes into application status object: %s", string(body))
	}

	defaultAppStatus := ServeApplicationStatus{
		Name:        "default",
		Message:     status.ApplicationStatus.Message,
		Status:      status.ApplicationStatus.Status,
		Deployments: make(map[string]ServeDeploymentStatus),
	}

	for _, deployment := range status.DeploymentStatuses {
		deploymentStatus := ServeDeploymentStatus{
			Status:  deployment.Status,
			Message: deployment.Message,
		}

		defaultAppStatus.Deployments[deployment.Name] = deploymentStatus
	}
	return &defaultAppStatus, nil
}

func (r *RayDashboardClient) GetMultiApplicationStatus(ctx context.Context) (map[string]*ServeApplicationStatus, error) {
	serveDetails, err := r.GetServeDetails(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed to get serve details: %v", err)
	}

	return r.ConvertServeDetailsToApplicationStatuses(serveDetails)
}

// GetServeDetails gets details on all live applications on the Ray cluster.
func (r *RayDashboardClient) GetServeDetails(ctx context.Context) (*ServeDetails, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", r.dashboardURL+ServeDetailsPath, nil)
	if err != nil {
		return nil, err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("GetServeDetails fail: %s %s", resp.Status, string(body))
	}

	var serveDetails ServeDetails
	if err = json.Unmarshal(body, &serveDetails); err != nil {
		return nil, fmt.Errorf("GetServeDetails failed. Failed to unmarshal bytes: %s", string(body))
	}

	return &serveDetails, nil
}

func (r *RayDashboardClient) ConvertServeDetailsToApplicationStatuses(serveDetails *ServeDetails) (map[string]*ServeApplicationStatus, error) {
	detailsJson, err := json.Marshal(serveDetails.Applications)
	if err != nil {
		return nil, fmt.Errorf("Failed to marshal serve details: %v.", serveDetails.Applications)
	}

	applicationStatuses := map[string]*ServeApplicationStatus{}
	if err = json.Unmarshal(detailsJson, &applicationStatuses); err != nil {
		return nil, fmt.Errorf("Failed to unmarshal serve details bytes into map of application statuses: %v. Bytes: %s", err, string(detailsJson))
	}

	return applicationStatuses, nil
}

func (r *RayDashboardClient) ConvertServeConfig(specs []rayv1alpha1.ServeConfigSpec) []ServeConfigSpec {
	serveConfigToSend := make([]ServeConfigSpec, len(specs))

	for i, config := range specs {
		userConfig := make(map[string]interface{})
		_ = yaml.Unmarshal([]byte(config.UserConfig), &userConfig)

		autoscalingConfig := make(map[string]interface{})
		_ = yaml.Unmarshal([]byte(config.AutoscalingConfig), &autoscalingConfig)

		runtimeEnv := make(map[string]interface{})
		_ = yaml.Unmarshal([]byte(config.RayActorOptions.RuntimeEnv), &runtimeEnv)

		resources := make(map[string]interface{})
		_ = yaml.Unmarshal([]byte(config.RayActorOptions.Resources), &resources)

		serveConfigToSend[i] = ServeConfigSpec{
			Name:                      config.Name,
			NumReplicas:               config.NumReplicas,
			RoutePrefix:               config.RoutePrefix,
			MaxConcurrentQueries:      config.MaxConcurrentQueries,
			UserConfig:                userConfig,
			AutoscalingConfig:         autoscalingConfig,
			GracefulShutdownWaitLoopS: config.GracefulShutdownWaitLoopS,
			GracefulShutdownTimeoutS:  config.GracefulShutdownTimeoutS,
			HealthCheckPeriodS:        config.HealthCheckPeriodS,
			HealthCheckTimeoutS:       config.GracefulShutdownTimeoutS,
			RayActorOptions: RayActorOptionSpec{
				RuntimeEnv:        runtimeEnv,
				NumCpus:           config.RayActorOptions.NumCpus,
				NumGpus:           config.RayActorOptions.NumGpus,
				Memory:            config.RayActorOptions.Memory,
				ObjectStoreMemory: config.RayActorOptions.ObjectStoreMemory,
				Resources:         resources,
				AcceleratorType:   config.RayActorOptions.AcceleratorType,
			},
		}
	}

	return serveConfigToSend
}

// RayJobInfo is the response of "ray job status" api.
// Reference to https://docs.ray.io/en/latest/cluster/jobs-package-ref.html#jobinfo.
type RayJobInfo struct {
	JobStatus  rayv1alpha1.JobStatus `json:"status,omitempty"`
	Entrypoint string                `json:"entrypoint,omitempty"`
	Message    string                `json:"message,omitempty"`
	ErrorType  *string               `json:"error_type,omitempty"`
	StartTime  int64                 `json:"start_time,omitempty"`
	EndTime    int64                 `json:"end_time,omitempty"`
	Metadata   map[string]string     `json:"metadata,omitempty"`
}

// RayJobRequest is the request body to submit.
// Reference to https://docs.ray.io/en/latest/cluster/jobs-package-ref.html#jobsubmissionclient.
type RayJobRequest struct {
	Entrypoint string                 `json:"entrypoint"`
	JobId      string                 `json:"job_id,omitempty"`
	RuntimeEnv map[string]interface{} `json:"runtime_env,omitempty"`
	Metadata   map[string]string      `json:"metadata,omitempty"`
}

type RayJobResponse struct {
	JobId string `json:"job_id"`
}

type RayJobStopResponse struct {
	Stopped bool `json:"stopped"`
}

func (r *RayDashboardClient) GetJobInfo(ctx context.Context, jobId string) (*RayJobInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", r.dashboardURL+JobPath+jobId, nil)
	if err != nil {
		return nil, err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var jobInfo RayJobInfo
	if err = json.Unmarshal(body, &jobInfo); err != nil {
		// Maybe body is not valid json, raise an error with the body.
		return nil, fmt.Errorf("GetJobInfo fail: %s", string(body))
	}

	return &jobInfo, nil
}

func (r *RayDashboardClient) SubmitJob(ctx context.Context, rayJob *rayv1alpha1.RayJob, log *logr.Logger) (jobId string, err error) {
	request, err := ConvertRayJobToReq(rayJob)
	if err != nil {
		return "", err
	}
	rayJobJson, err := json.Marshal(request)
	if err != nil {
		return
	}
	log.Info("Submit a ray job", "rayJob", rayJob.Name, "jobInfo", string(rayJobJson))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.dashboardURL+JobPath, bytes.NewBuffer(rayJobJson))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)

	var jobResp RayJobResponse
	if err = json.Unmarshal(body, &jobResp); err != nil {
		// Maybe body is not valid json, raise an error with the body.
		return "", fmt.Errorf("SubmitJob fail: %s", string(body))
	}

	return jobResp.JobId, nil
}

func (r *RayDashboardClient) StopJob(ctx context.Context, jobName string, log *logr.Logger) (err error) {
	log.Info("Stop a ray job", "rayJob", jobName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.dashboardURL+JobPath+jobName+"/stop", nil)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)

	var jobStopResp RayJobStopResponse
	if err = json.Unmarshal(body, &jobStopResp); err != nil {
		return err
	}

	if !jobStopResp.Stopped {
		jobInfo, err := r.GetJobInfo(ctx, jobName)
		if err != nil {
			return err
		}
		// StopJob only returns an error when JobStatus is not in terminal states (STOPPED / SUCCEEDED / FAILED)
		if !rayv1alpha1.IsJobTerminal(jobInfo.JobStatus) {
			return fmt.Errorf("Failed to stopped job: %v", jobInfo)
		}
	}
	return nil
}

func ConvertRayJobToReq(rayJob *rayv1alpha1.RayJob) (*RayJobRequest, error) {
	req := &RayJobRequest{
		Entrypoint: rayJob.Spec.Entrypoint,
		Metadata:   rayJob.Spec.Metadata,
		JobId:      rayJob.Status.JobId,
	}
	if len(rayJob.Spec.RuntimeEnv) == 0 {
		return req, nil
	}
	decodeBytes, err := base64.StdEncoding.DecodeString(rayJob.Spec.RuntimeEnv)
	if err != nil {
		return nil, fmt.Errorf("Failed to decode runtimeEnv: %v: %v", rayJob.Spec.RuntimeEnv, err)
	}
	var runtimeEnv map[string]interface{}
	err = json.Unmarshal(decodeBytes, &runtimeEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal runtimeEnv: %v: %v", decodeBytes, err)
	}
	req.RuntimeEnv = runtimeEnv
	return req, nil
}
