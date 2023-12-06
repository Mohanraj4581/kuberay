package server

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/go-logr/zerologr"
	api "github.com/ray-project/kuberay/proto/go_client"
	"github.com/ray-project/kuberay/ray-operator/controllers/ray/utils"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/emptypb"
)

type RayServeSubmissionServiceServerOptions struct {
	CollectMetrics bool
}

// implements `type ClusterServiceServer interface` in cluster_grpc.pb.go
// ClusterServer is the server API for ClusterService service.
type RayServeSubmissionServiceServer struct {
	options       *RayServeSubmissionServiceServerOptions
	clusterServer *ClusterServer
	log           logr.Logger
	api.UnimplementedRayServeSubmissionServiceServer
}

// Create RayServeSubmissionServiceServer
func NewRayServeSubmissionServiceServer(clusterServer *ClusterServer, options *RayServeSubmissionServiceServerOptions) *RayServeSubmissionServiceServer {
	zl := zerolog.New(os.Stdout).Level(zerolog.DebugLevel)
	return &RayServeSubmissionServiceServer{clusterServer: clusterServer, options: options, log: zerologr.New(&zl).WithName("jobsubmissionservice")}
}

// Submit Serve applications
func (s *RayServeSubmissionServiceServer) SubmitServeApplications(ctx context.Context, req *api.SubmitRayServeApplicationsRequest) (*emptypb.Empty, error) {
	s.log.Info("RayServeSubmissionService submit job")
	clusterRequest := api.GetClusterRequest{Name: req.Clustername, Namespace: req.Namespace}
	url, err := s.getRayClusterURL(ctx, &clusterRequest)
	if err != nil {
		return nil, err
	}
	rayDashboardClient := utils.GetRayDashboardClientFunc()
	rayDashboardClient.InitClient(*url)
	err = rayDashboardClient.DeployApplication(ctx, req.Configyaml)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// Get serve cluster details
func (s *RayServeSubmissionServiceServer) GetServeApplications(ctx context.Context, req *api.GetRayServeApplicationsRequest) (*api.GetRayServeApplicationReply, error) {
	s.log.Info("RayServeSubmissionService get service")
	clusterRequest := api.GetClusterRequest{Name: req.Clustername, Namespace: req.Namespace}
	url, err := s.getRayClusterURL(ctx, &clusterRequest)
	if err != nil {
		return nil, err
	}
	rayDashboardClient := utils.GetRayDashboardClientFunc()
	rayDashboardClient.InitClient(*url)
	serveDetails, err := rayDashboardClient.GetServeDetails(ctx)
	if err != nil {
		return nil, err
	}
	return convertServeDetails(serveDetails), nil
}

// Delete serve applications
func (s *RayServeSubmissionServiceServer) DeleteRayServeApplications(ctx context.Context, req *api.DeleteRayServeApplicationsRequest) (*emptypb.Empty, error) {
	s.log.Info("RayServeSubmissionService delete job")
	clusterRequest := api.GetClusterRequest{Name: req.Clustername, Namespace: req.Namespace}
	url, err := s.getRayClusterURL(ctx, &clusterRequest)
	if err != nil {
		return nil, err
	}
	rayDashboardClient := utils.GetRayDashboardClientFunc()
	rayDashboardClient.InitClient(*url)
	err = rayDashboardClient.DeleteServeApplications(ctx)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// Internal method to get cluster for job operation
func (s *RayServeSubmissionServiceServer) getRayClusterURL(ctx context.Context, request *api.GetClusterRequest) (*string, error) {
	cls, err := s.clusterServer.GetCluster(ctx, request)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(cls.ClusterState) != "ready" {
		return nil, errors.New("cluster is not ready")
	}
	// We are hardcoding port to the default value - 8265, as API server does not allow to modify it
	url := request.Name + "-head-svc." + request.Namespace + ".svc.cluster.local:52365"
	return &url, nil
}

func convertServeDetails(details *utils.ServeDetails) *api.GetRayServeApplicationReply {
	reply := api.GetRayServeApplicationReply{}
	reply.DeployMode = details.DeployMode
	reply.ProxyLocation = details.ProxyLocation
	reply.ControllerInfo = convertControllerIfo(&details.ControllerInfo)
	reply.HttpOptions = convertHTTPOptions(&details.HTTPOptions)
	reply.GrpcOptions = convertGRPCOptions(&details.GRPCOptions)
	reply.Applications = make(map[string]*api.ServeApplicationDetails)
	for name, application := range details.Applications {
		reply.Applications[name] = convertServeApplicationDetails(&application)
	}
	reply.Proxies = make(map[string]*api.Proxy)
	for name, proxy := range details.Proxies {
		reply.Proxies[name] = convertProxy(&proxy)
	}
	return &reply
}

func convertServeApplicationDetails(detail *utils.ServeApplicationDetails) *api.ServeApplicationDetails {
	reply := api.ServeApplicationDetails{}
	reply.DocsPath = detail.DocsPath
	reply.LastDeployedTimeS = detail.LastDeployed
	reply.Message = detail.Message
	reply.Name = detail.Name
	reply.RoutePrefix = detail.RoutePrefix
	reply.Status = detail.Status
	reply.DeployedAppConfig = convertServeDeploymentDetails(&detail.Configuration)
	reply.Deployments = make(map[string]*api.DeploymentApplicationDetails)
	for name, d := range detail.Deployments {
		reply.Deployments[name] = convertDeploymentApplicationDetails(&d)
	}
	return &reply
}

func convertDeploymentApplicationDetails(details *utils.DeploymentApplicationDetails) *api.DeploymentApplicationDetails {
	reply := api.DeploymentApplicationDetails{}
	reply.DeploymentConfig = make(map[string]*api.DeploymentSchema)
	for name, d := range details.Configuration {
		reply.DeploymentConfig[name] = convertDeploymentSchema(&d)
	}
	reply.Message = details.Message
	reply.Name = details.Message
	reply.Status = details.Status
	reply.Replicas = make([]*api.Replica, len(details.Replicas))
	for index, replica := range details.Replicas {
		reply.Replicas[index] = convertReplica(&replica)
	}
	return &reply
}

func convertReplica(replica *utils.Replica) *api.Replica {
	reply := api.Replica{}
	reply.ActorId = replica.ActorId
	reply.ActorName = replica.ActorName
	reply.LogFilePath = replica.LogFile
	reply.NodeId = replica.NodeId
	reply.NodeIp = replica.NodeIp
	reply.Pid = replica.Pid
	reply.ReplicaId = replica.ReplicaId
	reply.StartTimeS = replica.StartTime
	reply.State = replica.State
	reply.WorkerId = replica.WorkerId
	return &reply
}

func convertServeDeploymentDetails(detail *utils.ServeDeploymentDetails) *api.ServeDeploymentDetails {
	reply := api.ServeDeploymentDetails{}
	reply.Args = detail.Args
	reply.Host = detail.Host
	reply.ImportPath = detail.ImportPath
	reply.Name = detail.Name
	reply.Port = detail.Port
	reply.RoutePrefix = detail.RoutePrefix
	reply.RuntimeEnv = detail.Runtime
	reply.Deployments = make([]*api.DeploymentSchema, len(detail.Deployments))
	for index, schema := range detail.Deployments {
		reply.Deployments[index] = convertDeploymentSchema(&schema)
	}
	return &reply
}

func convertStringInterfaceToStringString(si map[string]interface{}) map[string]string {
	result := map[string]string{}
	for key, value := range si {
		result[key] = value.(string)
	}

	return result
}

func convertDeploymentSchema(schema *utils.DeploymentSchema) *api.DeploymentSchema {
	reply := api.DeploymentSchema{}
	reply.AutoscalingConfig = convertStringInterfaceToStringString(schema.AutoScalingConfig)
	reply.GracefulShutdownTimeoutS = schema.GracefulShutdownTimeout
	reply.GracefulShutdownWaitLoopS = schema.GracefulShutdownLoop
	reply.HealthCheckPeriodS = schema.HealthCheckPeriod
	reply.HealthCheckTimeoutS = schema.HealthCheckTmout
	reply.IsDriverDeployment = schema.DriverDeployment
	reply.MaxConcurrentQueries = schema.MaxQueries
	reply.MaxReplicasPerNode = schema.MaxReplicasNode
	reply.Name = schema.Name
	reply.NumReplicas = schema.NumReplicas
	reply.PlacementGroupStrategy = schema.PLacementStrategy
	reply.RayActorOptions = &api.RayActorOptionSpec{
		RuntimeEnv:        convertStringInterfaceToStringString(schema.ActorOptions.RuntimeEnv),
		NumCpus:           float32(*schema.ActorOptions.NumCpus),
		NumGpus:           float32(*schema.ActorOptions.NumGpus),
		Memory:            *schema.ActorOptions.Memory,
		ObjectStoreMemory: *schema.ActorOptions.ObjectStoreMemory,
		Resources:         convertStringInterfaceToStringString(schema.ActorOptions.Resources),
		AcceleratorType:   schema.ActorOptions.AcceleratorType,
	}
	reply.RoutePrefix = schema.RoutePrefix
	reply.UserConfig = convertStringInterfaceToStringString(schema.UserConfig)
	reply.PlacementGroupBundles = make([]*api.PlacementGroupBundle, len(schema.PLacementBundles))
	for index, bundle := range schema.PLacementBundles {
		reply.PlacementGroupBundles[index] = &api.PlacementGroupBundle{Bundle: bundle}
	}
	return &reply
}

func convertControllerIfo(info *utils.ControllerInfo) *api.ControllerInfo {
	reply := api.ControllerInfo{}
	reply.ActorId = info.ActorId
	reply.ActorName = info.ActorName
	reply.LogFilePath = info.LogFilePath
	reply.NodeId = info.NodeId
	reply.NodeIp = info.NodeIp
	return &reply
}

func convertHTTPOptions(options *utils.HTTPOptions) *api.HTTPOptions {
	reply := api.HTTPOptions{}
	reply.Host = options.Host
	reply.KeepAliveTimeoutS = options.KeepAliveTimeout
	reply.Port = options.Port
	reply.RequestTimeoutS = options.RequestTimeout
	reply.RootPath = options.RootPath
	return &reply
}

func convertGRPCOptions(options *utils.GRPCOptions) *api.GRPCOptions {
	reply := api.GRPCOptions{}
	reply.Port = options.Port
	reply.GrpcServicerFunctions = options.ServerFunctions
	return &reply
}

func convertProxy(proxy *utils.Proxy) *api.Proxy {
	reply := api.Proxy{}
	reply.ActorId = proxy.ActorId
	reply.ActorName = proxy.ActorName
	reply.NodeId = proxy.NodeId
	reply.NodeIp = proxy.NodeIp
	reply.Status = proxy.Status
	reply.WorkerId = proxy.WorkerId
	return &reply
}
