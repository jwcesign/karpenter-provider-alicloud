package cluster

import (
	"context"
	"encoding/base64"
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"net/http"
)

const customClusterType = "Custom"

type Custom struct {
}

func NewCustom() *Custom {
	return &Custom{}
}

func (c *Custom) UserData(ctx context.Context, labels map[string]string, taints []corev1.Taint, configuration *v1alpha1.KubeletConfiguration, userData *string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(lo.FromPtr(userData))), nil
}

func (c *Custom) GetClusterCNI(ctx context.Context) (string, error) {
	return customClusterType, nil
}

func (c *Custom) LivenessProbe(request *http.Request) error {
	return nil
}

func (c *Custom) ClusterType() string {
	return customClusterType
}

func (c *Custom) GetSupportedImages(s string) ([]Image, error) {
	return nil, nil
}

func (c *Custom) FeatureFlags() FeatureFlags {
	return FeatureFlags{
		PodsPerCoreEnabled:           false,
		SupportsENILimitedPodDensity: false,
	}
}
