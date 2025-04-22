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
	"context"
	"encoding/base64"
	"mime"
	"strings"
	"testing"

	awsmime "github.com/aws/karpenter-provider-aws/pkg/providers/amifamily/bootstrap/mime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	. "sigs.k8s.io/karpenter/pkg/utils/testing"
)

var ctx context.Context

func TestClusterProvider(t *testing.T) {
	ctx = TestContextWithLogger(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Validation")
}

var _ = Describe("CloudInit", func() {
	var cloudInit *CloudInit

	BeforeEach(func() {
		cloudInit = NewCloudInit()
	})

	Describe("NewCloudInit", func() {
		It("should initialize empty entries", func() {
			Expect(cloudInit.entries).To(BeEmpty())
		})
	})

	Describe("Merge", func() {
		Context("when merging empty userdata", func() {
			It("should do nothing", func() {
				err := cloudInit.Merge(lo.ToPtr(""))
				Expect(err).NotTo(HaveOccurred())
				Expect(cloudInit.entries).To(BeEmpty())
			})
		})

		Context("when merging valid MIME data", func() {
			mimeData := `Content-Type: multipart/mixed; boundary="//"
MIME-Version: 1.0

--//
Content-Type: text/x-shellscript; stage=pre

#!/bin/bash
echo 'pre-script'

--//
Content-Type: text/x-shellscript"

#!/bin/bash
echo 'http://localhost'

--//
Content-Type: text/x-shellscript; stage=pre

#!/bin/sh
echo 'The time is now $(date -R)!'

--//
Content-Type: text/cloud-config; charset="us-ascii"

fqdn: my-instance.localdomain

--//--
`

			It("should parse and append entries", func() {
				err := cloudInit.Merge(lo.ToPtr(mimeData))
				Expect(err).NotTo(HaveOccurred())
				Expect(cloudInit.entries).To(HaveLen(4))
				mediaType, _, err := mime.ParseMediaType(string(cloudInit.entries[2].ContentType))
				Expect(err).NotTo(HaveOccurred())
				Expect(mediaType).To(Equal(contentTypeShellScriptMediaType))
				Expect(cloudInit.entries[2].Content).To(Equal("#!/bin/sh\necho 'The time is now $(date -R)!'\n"))
			})
		})

		Context("when merging invalid MIME data", func() {
			invalidData := "MIME-Version: 1.0\nInvalid-Content"

			It("should return error", func() {
				err := cloudInit.Merge(lo.ToPtr(invalidData))
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when merging non-MIME shell script", func() {
			script := "#!/bin/bash\necho 'hello'"

			It("should append as shell script entry", func() {
				err := cloudInit.Merge(lo.ToPtr(script))
				Expect(err).NotTo(HaveOccurred())
				Expect(cloudInit.entries).To(HaveLen(1))

				entry := cloudInit.entries[0]
				Expect(entry.ContentType).To(Equal(awsmime.ContentTypeShellScript))
				Expect(entry.Content).To(Equal(script))
			})
		})
	})

	Describe("Script", func() {
		Context("when entries are empty", func() {
			It("should return empty string", func() {
				result, err := cloudInit.Script()
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal("TUlNRS1WZXJzaW9uOiAxLjAKQ29udGVudC1UeXBlOiBtdWx0aXBhcnQvbWl4ZWQ7IGJvdW5kYXJ5PSIvLyIKCgotLS8vLS0K"))
			})
		})

		Context("with multiple entries", func() {

			It("should sort entries and serialize", func() {
				cloudInit.entries = []awsmime.Entry{
					{
						ContentType: awsmime.ContentTypeShellScript,
						Content:     "normal-script",
					},
					{
						ContentType: awsmime.ContentTypeShellScript + "; stage=pre",
						Content:     "pre-script",
					},
				}
				// Mock awsmime.Archive if needed
				result, err := cloudInit.Script()
				Expect(err).NotTo(HaveOccurred())
				arr, err := base64.StdEncoding.DecodeString(result)
				Expect(err).NotTo(HaveOccurred())
				result = string(arr)

				// Verify MIME structure contains sorted entries
				Expect(result).To(ContainSubstring("pre-script"))
				Expect(result).To(ContainSubstring("normal-script"))
				Expect(strings.Index(result, "pre-script")).To(BeNumerically("<",
					strings.Index(result, "normal-script")))
			})
		})
	})

	Describe("sort", func() {
		It("should order pre-stage entries first", func() {
			entries := []awsmime.Entry{
				{ContentType: "text/x-shellscript", Content: "normal"},
				{ContentType: "text/x-shellscript; stage=pre", Content: "pre1"},
				{ContentType: "text/plain", Content: "other"},
				{ContentType: "text/x-shellscript; stage=pre", Content: "pre2"},
			}

			cloudInit.entries = entries
			cloudInit.sort()

			Expect(cloudInit.entries).To(HaveLen(4))
			Expect(cloudInit.entries[0].Content).To(Equal("pre1"))
			Expect(cloudInit.entries[1].Content).To(Equal("pre2"))
			Expect(cloudInit.entries[2].Content).To(Equal("normal"))
			Expect(cloudInit.entries[3].Content).To(Equal("other"))
		})

		Context("with invalid content types", func() {
			It("should treat as non-pre entries", func() {
				entries := []awsmime.Entry{
					{ContentType: "invalid/type", Content: "invalid"},
					{ContentType: "text/x-shellscript; stage=pre", Content: "pre"},
				}

				cloudInit.entries = entries
				cloudInit.sort()

				Expect(cloudInit.entries[0].Content).To(Equal("pre"))
				Expect(cloudInit.entries[1].Content).To(Equal("invalid"))
			})
		})
	})

	Describe("build userdata", func() {
		var bootstrap string

		BeforeEach(func() {
			bootstrap = "#!/bin/bash\necho 'start ack'\n"
		})

		It("merge with custom userdata", func() {
			customUserData := `Content-Type: multipart/mixed; boundary="//"
MIME-Version: 1.0

--//
Content-Type: text/cloud-config; charset="us-ascii"

fqdn: my-instance.localdomain

--//
Content-Type: text/x-shellscript; stage=pre

#!/bin/bash
echo 'pre-script'

--//
Content-Type: text/x-shellscript

#!/bin/bash
echo 'http://localhost'

--//--
`

			Expect(cloudInit.Merge(&bootstrap)).Should(Succeed())
			Expect(cloudInit.Merge(&customUserData)).Should(Succeed())
			encodeScript, err := cloudInit.Script()
			Expect(err).NotTo(HaveOccurred())
			result, err := base64.StdEncoding.DecodeString(encodeScript)
			Expect(err).NotTo(HaveOccurred())

			Expect(string(result)).To(Equal(`MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="//"

--//
Content-Type: text/x-shellscript; stage=pre

#!/bin/bash
echo 'pre-script'

--//
Content-Type: text/x-shellscript; charset="us-ascii"

#!/bin/bash
echo 'start ack'

--//
Content-Type: text/cloud-config; charset="us-ascii"

fqdn: my-instance.localdomain

--//
Content-Type: text/x-shellscript

#!/bin/bash
echo 'http://localhost'

--//--
`))
		})

		It("merge with empty userdata", func() {

			Expect(cloudInit.Merge(&bootstrap)).Should(Succeed())
			Expect(cloudInit.Merge(nil)).Should(Succeed())
			encodeScript, err := cloudInit.Script()
			Expect(err).NotTo(HaveOccurred())
			result, err := base64.StdEncoding.DecodeString(encodeScript)
			Expect(err).NotTo(HaveOccurred())

			Expect(string(result)).To(Equal(`MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="//"

--//
Content-Type: text/x-shellscript; charset="us-ascii"

#!/bin/bash
echo 'start ack'

--//--
`))
		})

		It("merge with shell userdata", func() {
			customUserData := "echo 'hello'\necho 'world'"

			Expect(cloudInit.Merge(&bootstrap)).Should(Succeed())
			Expect(cloudInit.Merge(&customUserData)).Should(Succeed())
			encodeScript, err := cloudInit.Script()
			Expect(err).NotTo(HaveOccurred())
			result, err := base64.StdEncoding.DecodeString(encodeScript)
			Expect(err).NotTo(HaveOccurred())

			Expect(string(result)).To(Equal(`MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="//"

--//
Content-Type: text/x-shellscript; charset="us-ascii"

#!/bin/bash
echo 'start ack'

--//
Content-Type: text/x-shellscript; charset="us-ascii"

echo 'hello'
echo 'world'
--//--
`))
		})
	})
})
