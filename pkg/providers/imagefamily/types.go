/*
Copyright 2024 The CloudPilot AI Authors.

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

package imagefamily

import (
	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/providers/cluster"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

type Image struct {
	Name         string
	ImageID      string
	Requirements scheduling.Requirements
}

type Images []Image

// ImageFamily can be implemented to override the default logic for generating dynamic launch template parameters
type ImageFamily interface {
	GetImages(supportedImages []cluster.Image, kubernetesVersion, imageVersion string) (Images, error)
}
