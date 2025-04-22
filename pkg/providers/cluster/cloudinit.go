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

package cluster

import (
	"mime"
	"strings"

	awsmime "github.com/aws/karpenter-provider-aws/pkg/providers/amifamily/bootstrap/mime"
	"github.com/samber/lo"
)

const (
	contentTypeStage                string = `stage`
	contentTypeShellScriptMediaType string = `text/x-shellscript`
)

type CloudInit struct {
	entries []awsmime.Entry
}

func NewCloudInit() *CloudInit {
	return &CloudInit{
		entries: make([]awsmime.Entry, 0),
	}
}

func (c *CloudInit) Script() (string, error) {
	c.sort()
	mimeArchive := awsmime.Archive(c.entries)
	userData, err := mimeArchive.Serialize()
	if err != nil {
		return "", err
	}
	return userData, nil
}

func (c *CloudInit) Merge(userdata *string) error {
	userData := lo.FromPtr(userdata)
	if userData == "" {
		return nil
	}
	if strings.HasPrefix(strings.TrimSpace(userData), "MIME-Version:") ||
		strings.HasPrefix(strings.TrimSpace(userData), "Content-Type:") {
		archive, err := awsmime.NewArchive(userData)
		if err != nil {
			return err
		}
		c.entries = append(c.entries, archive...)
		return nil
	}
	// Fallback to YAML or shall script if UserData is not in MIME format. Determine the content type for the
	// generated MIME header depending on the type of the custom UserData.
	c.entries = append(c.entries, awsmime.Entry{
		ContentType: awsmime.ContentTypeShellScript,
		Content:     userData,
	})
	return nil
}

func (c *CloudInit) sort() {
	var pre, non []awsmime.Entry
	for _, entry := range c.entries {
		mediaType, params, err := mime.ParseMediaType(string(entry.ContentType))
		if err != nil {
			non = append(non, entry)
			continue
		}
		if stage, ok := params[contentTypeStage]; ok && mediaType == contentTypeShellScriptMediaType && stage == "pre" {
			pre = append(pre, entry)
		} else {
			non = append(non, entry)
		}
	}
	c.entries = append(pre, non...)
}
