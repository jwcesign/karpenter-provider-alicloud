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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	ackclient "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/cloudpilot-ai/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
)

const defaultNodeLabel = "k8s.aliyun.com=true"

type Provider interface {
	GetNodeRegisterScript(context.Context, string, *karpv1.NodeClaim, *v1alpha1.KubeletConfiguration, *string) (string, error)
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
	capacityType string,
	nodeClaim *karpv1.NodeClaim,
	kubeletCfg *v1alpha1.KubeletConfiguration,
	userData *string) (string, error) {
	labels := lo.Assign(nodeClaim.Labels, map[string]string{karpv1.CapacityTypeLabelKey: capacityType})
	if cachedScript, ok := p.cache.Get(p.clusterID); ok {
		return p.resolveUserData(cachedScript.(string), labels, nodeClaim, kubeletCfg, userData), nil
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
	return p.resolveUserData(s, labels, nodeClaim, kubeletCfg, userData), nil
}

func (p *DefaultProvider) resolveUserData(respStr string, labels map[string]string, nodeClaim *karpv1.NodeClaim,
	kubeletCfg *v1alpha1.KubeletConfiguration, userData *string) string {
	preUserData, postUserData := parseCustomUserData(userData)

	var script bytes.Buffer
	// Add bash script header
	script.WriteString("#!/bin/bash\n\n")

	// Insert preUserData if available
	if preUserData != "" {
		// Pre-userData: scripts to be executed before node registration
		script.WriteString("echo \"Executing preUserData...\"\n")
		script.WriteString(preUserData + "\n\n")
	}

	// Clean up the input string
	cleanupStr := strings.ReplaceAll(respStr, "\r\n", "")
	script.WriteString(cleanupStr + " ")
	// Add labels
	script.WriteString(fmt.Sprintf("--labels %s ", p.formatLabels(labels)))
	// Add kubelet config
	cfg := convertNodeClassKubeletConfigToACKNodeConfig(kubeletCfg)
	script.WriteString(fmt.Sprintf("--node-config %s ", cfg))
	// Add taints
	script.WriteString(fmt.Sprintf("--taints %s\n\n", p.formatTaints(nodeClaim)))

	// Insert postUserData if available
	if postUserData != "" {
		// Post-userData: scripts to be executed after node registration
		script.WriteString("echo \"Executing postUserData...\"\n")
		script.WriteString(postUserData + "\n")
	}

	// Encode to base64
	return base64.StdEncoding.EncodeToString(script.Bytes())
}

func (p *DefaultProvider) formatLabels(labels map[string]string) string {
	labelsFormatted := fmt.Sprintf("%s,ack.aliyun.com=%s", defaultNodeLabel, p.clusterID)
	for key, value := range labels {
		labelsFormatted = fmt.Sprintf("%s,%s=%s", labelsFormatted, key, value)
	}
	return labelsFormatted
}

func (p *DefaultProvider) formatTaints(nodeClaim *karpv1.NodeClaim) string {
	taints := lo.Flatten([][]corev1.Taint{
		nodeClaim.Spec.Taints,
		nodeClaim.Spec.StartupTaints,
	})
	if !lo.ContainsBy(taints, func(t corev1.Taint) bool {
		return t.MatchTaint(&karpv1.UnregisteredNoExecuteTaint)
	}) {
		taints = append(taints, karpv1.UnregisteredNoExecuteTaint)
	}
	return strings.Join(lo.Map(taints, func(t corev1.Taint, _ int) string {
		return t.ToString()
	}), ",")
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

const userDataSeparator = "#===USERDATA_SEPARATOR==="

// By default, the UserData is executed after the node registration is completed.
// If a user requires tasks to be executed both before and after node registration,
// they must split the userdata into preUserData and postUserData using a SEPARATOR.
func parseCustomUserData(userData *string) (string, string) {
	parts := strings.Split(tea.StringValue(userData), userDataSeparator)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return "", tea.StringValue(userData)
}
