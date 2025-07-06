<div style="text-align: center">
  <p align="center">
    <img src="docs/images/banner.png" height="200">
    <br><br>
    <i>Autoscale ACK cluster nodes efficiently and cost-effectively.</i>
  </p>
</div>

![GitHub stars](https://img.shields.io/github/stars/cloudpilot-ai/karpenter-provider-alibabacloud)
![GitHub forks](https://img.shields.io/github/forks/cloudpilot-ai/karpenter-provider-alibabacloud)
[![GitHub License](https://img.shields.io/badge/License-Apache%202.0-ff69b4.svg)](https://github.com/cloudpilot-ai/karpenter-provider-alibabacloud/blob/main/LICENSE)
[![contributions welcome](https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat)](https://github.com/cloudpilot-ai/karpenter-provider-alibabacloud/issues)

## Introduction

Karpenter is an open-source node provisioning project built for Kubernetes.
Karpenter improves the efficiency and cost of running workloads on Kubernetes clusters by:

* **Watching** for pods that the Kubernetes scheduler has marked as unschedulable
* **Evaluating** scheduling constraints (resource requests, nodeselectors, affinities, tolerations, and topology spread constraints) requested by the pods
* **Provisioning** nodes that meet the requirements of the pods
* **Removing** the nodes when the nodes are no longer needed

## How it works

Karpenter observes the aggregate resource requests of unscheduled pods and makes decisions to launch and terminate nodes to minimize scheduling latencies and infrastructure cost.

<div style="text-align: center">
  <p align="center">
    <img src="docs/images/karpenter-overview.jpg" width="100%">
  </p>
</div>

## Getting started

* [Introduction](https://docs.cloudpilot.ai/karpenter/alibabacloud)
* [Installation](https://docs.cloudpilot.ai/karpenter/alibabacloud/installation)

## Documentation

Full documentation is available at [karpenter alibabacloud provider docs](https://docs.cloudpilot.ai/karpenter/alibabacloud/).

## Community

We want your contributions and suggestions! One of the easiest ways to contribute is to participate in discussions on the Github Issues/Discussion, chat on IM or the bi-weekly community calls.

* [Slack channel](https://cloudpilotaicommunity.slack.com/archives/C093V65481H)
* [Discord](https://discord.gg/WxFWc87QWr)
* WeChat Group: Scan and you will be invited to join

  <img src="docs/images/wechat-group-alicloud-provider.png" width="50%">

## Attribution Notice

This project includes code derived from karpenter-provider-aws, used under the Apache License, Version 2.0 terms. We acknowledge the contributions of the original authors and thank them for making their work available. For more details, see the [karpenter-provider-aws](https://github.com/aws/karpenter-provider-aws).

## Code Of Conduct

Karpenter Alibaba Cloud Provider adopts [CNCF code of conduct](https://github.com/cncf/foundation/blob/master/code-of-conduct.md).

## License

Karpenter Alibaba Cloud Provider is under the Apache 2.0 license. See the [LICENSE](LICENSE) file for details.
