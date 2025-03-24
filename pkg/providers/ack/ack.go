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

package ack

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	ackclient "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/patrickmn/go-cache"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
)

const defaultNodeLabel = "k8s.aliyun.com=true"

type Provider interface {
	GetNodeRegisterScript(context.Context, map[string]string, *v1alpha1.KubeletConfiguration) (string, error)
	GetClusterCNI(context.Context) (string, error)
	LivenessProbe(*http.Request) error
}

type DefaultProvider struct {
	clusterID string
	ackClient *ackclient.Client

	muClusterCNI sync.RWMutex
	clusterCNI   string
	cache        *cache.Cache
}

func NewDefaultProvider(clusterID string, ackClient *ackclient.Client, cache *cache.Cache) *DefaultProvider {
	return &DefaultProvider{
		clusterID: clusterID,
		ackClient: ackClient,
		cache:     cache,
	}
}

func (p *DefaultProvider) LivenessProbe(_ *http.Request) error {
	p.muClusterCNI.Lock()
	//nolint: staticcheck
	p.muClusterCNI.Unlock()
	return nil
}

func (p *DefaultProvider) GetClusterCNI(_ context.Context) (string, error) {
	p.muClusterCNI.RLock()
	clusterCNI := p.clusterCNI
	p.muClusterCNI.RUnlock()

	if clusterCNI != "" {
		return clusterCNI, nil
	}

	response, err := p.ackClient.DescribeClusterDetail(tea.String(p.clusterID))
	if err != nil {
		return "", fmt.Errorf("failed to describe cluster: %w", err)
	}
	if response == nil || response.Body == nil || response.Body.MetaData == nil {
		return "", fmt.Errorf("empty cluster response")
	}
	// Parse metadata JSON string
	// clusterMetaData represents the metadata structure in cluster response
	type clusterMetaData struct {
		Capabilities struct {
			Network string `json:"Network"`
		} `json:"Capabilities"`
	}
	var metadata clusterMetaData
	if err := json.Unmarshal([]byte(*response.Body.MetaData), &metadata); err != nil {
		return "", fmt.Errorf("failed to unmarshal cluster metadata: %w", err)
	}

	p.muClusterCNI.Lock()
	p.clusterCNI = metadata.Capabilities.Network
	p.muClusterCNI.Unlock()

	return p.clusterCNI, nil
}

// Get the ID of the target nodepool id when DescribeClusterAttachScriptsRequest.
// If there is no default nodepool, select the nodepool with the most HealthyNodes.
//
//nolint:gocyclo
func (p *DefaultProvider) getTargetNodePoolID(ctx context.Context) (*string, error) {
	resp, err := p.ackClient.DescribeClusterNodePools(tea.String(p.clusterID), &ackclient.DescribeClusterNodePoolsRequest{})
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to describe cluster nodepools")
		return nil, err
	}
	if resp == nil || resp.Body == nil || resp.Body.Nodepools == nil {
		return nil, fmt.Errorf("empty describe cluster nodepools response")
	}
	if len(resp.Body.Nodepools) == 0 {
		return nil, fmt.Errorf("no nodepool found")
	}

	nodepools := resp.Body.Nodepools
	sort.Slice(nodepools, func(i, j int) bool {
		if nodepools[i].NodepoolInfo == nil || nodepools[j].NodepoolInfo == nil {
			return false
		}

		if nodepools[i].NodepoolInfo.IsDefault != nil && nodepools[j].NodepoolInfo.IsDefault != nil {
			if *nodepools[i].NodepoolInfo.IsDefault && !*nodepools[j].NodepoolInfo.IsDefault {
				return true
			}
			if !*nodepools[i].NodepoolInfo.IsDefault && *nodepools[j].NodepoolInfo.IsDefault {
				return false
			}
		}

		if nodepools[i].Status == nil || nodepools[j].Status == nil || nodepools[i].Status.HealthyNodes == nil || nodepools[j].Status.HealthyNodes == nil {
			return false
		}
		return *nodepools[i].Status.HealthyNodes > *nodepools[j].Status.HealthyNodes
	})

	targetNodepool := nodepools[0]
	if targetNodepool.NodepoolInfo == nil {
		return nil, fmt.Errorf("target describe cluster nodepool is empty")
	}
	return targetNodepool.NodepoolInfo.NodepoolId, nil
}

func (p *DefaultProvider) GetNodeRegisterScript(ctx context.Context,
	labels map[string]string,
	kubeletCfg *v1alpha1.KubeletConfiguration) (string, error) {
	if cachedScript, ok := p.cache.Get(p.clusterID); ok {
		return p.resolveUserData(cachedScript.(string), labels, kubeletCfg), nil
	}

	nodepoolID, err := p.getTargetNodePoolID(ctx)
	if err != nil {
		// Don't return here, we can process when there is no default cluster id.
		// We need to try to obtain a usable nodepool ID in order to get the cluster attach scripts.
		// One known scenario is on an ACK cluster with version 1.24, where the user deleted the default nodepool and
		// created a nodepool with a containerd runtime. The DescribeClusterAttachScriptsRequest api will use the
		// CRI configuration of the deleted default nodepool, which might be using the Docker runtime.
		// This could result in nodes failing to register to the new cluster.
		log.FromContext(ctx).Error(err, "Failed to get default nodepool id")
	}
	reqPara := &ackclient.DescribeClusterAttachScriptsRequest{
		KeepInstanceName: tea.Bool(true),
		NodepoolId:       nodepoolID,
	}
	resp, err := p.ackClient.DescribeClusterAttachScripts(tea.String(p.clusterID), reqPara)
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to get node registration script")
		return "", err
	}
	s := tea.StringValue(resp.Body)
	if s == "" {
		err := errors.New("empty node registration script")
		log.FromContext(ctx).Error(err, "")
		return "", err
	}

	p.cache.SetDefault(p.clusterID, s)
	return p.resolveUserData(s, labels, kubeletCfg), nil
}

func (p *DefaultProvider) resolveUserData(respStr string,
	labels map[string]string,
	kubeletCfg *v1alpha1.KubeletConfiguration) string {
	cleanupStr := strings.ReplaceAll(respStr, "\r\n", "")

	// TODO: now, the following code is quite ugly, make it clean in the future
	// Add labels
	labelsFormated := fmt.Sprintf("%s,ack.aliyun.com=%s", defaultNodeLabel, p.clusterID)
	for labelKey, labelValue := range labels {
		labelsFormated = fmt.Sprintf("%s,%s=%s", labelsFormated, labelKey, labelValue)
	}
	re := regexp.MustCompile(`--labels\s+\S+`)
	updatedCommand := re.ReplaceAllString(cleanupStr, "--labels "+labelsFormated)

	// Add kubelet config
	cfg := convertNodeClassKubeletConfigToACKNodeConfig(kubeletCfg)
	updatedCommand = fmt.Sprintf("%s --node-config %s", updatedCommand, cfg)

	// Add taints
	taint := karpv1.UnregisteredNoExecuteTaint
	updatedCommand = fmt.Sprintf("%s --taints %s", updatedCommand, taint.ToString())

	// Add bash script header
	finalScript := fmt.Sprintf("#!/bin/bash\n\n%s", updatedCommand)

	return base64.StdEncoding.EncodeToString([]byte(finalScript))
}

type NodeConfig struct {
	KubeletConfig *ACKKubeletConfig `json:"kubelet_config,omitempty"`
}

type ACKKubeletConfig struct {
	MaxPods *int32 `json:"maxPods,omitempty"`
}

func convertNodeClassKubeletConfigToACKNodeConfig(kubeletCfg *v1alpha1.KubeletConfiguration) string {
	cfg := &NodeConfig{
		KubeletConfig: &ACKKubeletConfig{
			MaxPods: kubeletCfg.MaxPods,
		},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return base64.StdEncoding.EncodeToString([]byte("{}"))
	}
	return base64.StdEncoding.EncodeToString(data)
}
