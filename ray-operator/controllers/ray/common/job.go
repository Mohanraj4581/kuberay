package common

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"github.com/google/shlex"
	rayv1alpha1 "github.com/ray-project/kuberay/ray-operator/apis/ray/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// GetDecodedRuntimeEnv decodes the runtime environment for the Ray job from a base64-encoded string.
func GetDecodedRuntimeEnv(runtimeEnv string) (string, error) {
	decodedBytes, err := base64.StdEncoding.DecodeString(runtimeEnv)
	if err != nil {
		return "", fmt.Errorf("failed to decode runtimeEnv: %v: %v", runtimeEnv, err)
	}
	return string(decodedBytes), nil
}

// GetRuntimeEnvJson returns the JSON string of the runtime environment for the Ray job.
func getRuntimeEnvJson(rayJobInstance *rayv1alpha1.RayJob) (string, error) {
	runtimeEnv := rayJobInstance.Spec.RuntimeEnv
	runtimeEnvYAML := rayJobInstance.Spec.RuntimeEnvYAML

	// Check if both runtimeEnv and RuntimeEnvYAML are specified.
	if len(runtimeEnv) > 0 && len(runtimeEnvYAML) > 0 {
		return "", fmt.Errorf("Both runtimeEnv and RuntimeEnvYAML are specified. Please specify only one of the fields.")
	}

	if len(runtimeEnv) > 0 {
		return GetDecodedRuntimeEnv(runtimeEnv)
	}

	if len(runtimeEnvYAML) > 0 {
		// Convert YAML to JSON
		jsonData, err := yaml.YAMLToJSON([]byte(runtimeEnvYAML))
		if err != nil {
			return "", err
		}
		// We return the JSON as a string
		return string(jsonData), nil
	}

	return "", nil
}

// GetBaseRayJobCommand returns the first part of the Ray Job command up to and including the address, e.g. "ray job submit --address http://..."
func GetBaseRayJobCommand(address string) []string {
	// add http:// if needed
	if !strings.HasPrefix(address, "http://") {
		address = "http://" + address
	}
	return []string{"ray", "job", "submit", "--address", address}
}

// GetMetadataJson returns the JSON string of the metadata for the Ray job.
func GetMetadataJson(metadata map[string]string, rayVersion string) (string, error) {
	// Check that the Ray version is at least 2.6.0.
	// If it is, we can use the --metadata-json flag.
	// Otherwise, we need to raise an error.
	constraint, _ := semver.NewConstraint(">= 2.6.0")
	v, err := semver.NewVersion(rayVersion)
	if err != nil {
		return "", fmt.Errorf("failed to parse Ray version: %v: %v", rayVersion, err)
	}
	if !constraint.Check(v) {
		return "", fmt.Errorf("the Ray version must be at least 2.6.0 to use the metadata field")
	}
	// Convert the metadata map to a JSON string.
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("failed to marshal metadata: %v: %v", metadata, err)
	}
	return string(metadataBytes), nil
}

// GetK8sJobCommand builds the K8s job command for the Ray job.
func GetK8sJobCommand(rayJobInstance *rayv1alpha1.RayJob) ([]string, error) {
	address := rayJobInstance.Status.DashboardURL
	metadata := rayJobInstance.Spec.Metadata
	jobId := rayJobInstance.Status.JobId
	entrypoint := rayJobInstance.Spec.Entrypoint

	k8sJobCommand := GetBaseRayJobCommand(address)

	runtimeEnvJson, err := getRuntimeEnvJson(rayJobInstance)
	if err != nil {
		return nil, err
	}
	if len(runtimeEnvJson) > 0 {
		k8sJobCommand = append(k8sJobCommand, "--runtime-env-json", runtimeEnvJson)
	}

	if len(metadata) > 0 {
		metadataJson, err := GetMetadataJson(metadata, rayJobInstance.Spec.RayClusterSpec.RayVersion)
		if err != nil {
			return nil, err
		}
		k8sJobCommand = append(k8sJobCommand, "--metadata-json", metadataJson)
	}

	if len(jobId) > 0 {
		k8sJobCommand = append(k8sJobCommand, "--submission-id", jobId)
	}

	// "--" is used to separate the entrypoint from the Ray Job CLI command and its arguments.
	k8sJobCommand = append(k8sJobCommand, "--")

	commandSlice, err := shlex.Split(entrypoint)
	if err != nil {
		return nil, err
	}
	k8sJobCommand = append(k8sJobCommand, commandSlice...)

	return k8sJobCommand, nil
}

// getDefaultSubmitterTemplate creates a default submitter template for the Ray job.
func GetDefaultSubmitterTemplate(rayJobInstance *rayv1alpha1.RayJob) v1.PodTemplateSpec {
	// Use the image of the Ray head to be defensive against version mismatch issues
	var image string
	if rayJobInstance.Spec.RayClusterSpec != nil &&
		len(rayJobInstance.Spec.RayClusterSpec.HeadGroupSpec.Template.Spec.Containers) > 0 {
		image = rayJobInstance.Spec.RayClusterSpec.HeadGroupSpec.Template.Spec.Containers[0].Image
	}

	if len(image) == 0 {
		// If we can't find the image of the Ray head, fall back to the latest stable release.
		image = "rayproject/ray:latest"
	}
	return v1.PodTemplateSpec{
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "ray-job-submitter",
					Image: image,
				},
			},
			RestartPolicy: v1.RestartPolicyNever,
		},
	}
}
