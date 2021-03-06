/*
Copyright 2017 The Kubernetes Authors.

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

package e2e_node

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/pkg/api"
	"k8s.io/kubernetes/pkg/api/v1"
	"k8s.io/kubernetes/pkg/apis/componentconfig"
	"k8s.io/kubernetes/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const acceleratorsFeatureGate = "Accelerators=true"

// Serial because the test updates kubelet configuration.
var _ = framework.KubeDescribe("GPU [Serial]", func() {
	f := framework.NewDefaultFramework("gpu-test")
	Context("attempt to use GPUs if available", func() {
		It("setup the node and create pods to test gpus", func() {
			By("ensuring that dynamic kubelet configuration is enabled")
			enabled, err := isKubeletConfigEnabled(f)
			framework.ExpectNoError(err)
			if !enabled {
				Skip("Dynamic Kubelet configuration is not enabled. Skipping test.")
			}

			By("enabling support for GPUs")
			var oldCfg *componentconfig.KubeletConfiguration
			defer func() {
				if oldCfg != nil {
					framework.ExpectNoError(setKubeletConfiguration(f, oldCfg))
				}
			}()

			oldCfg, err = getCurrentKubeletConfig()
			framework.ExpectNoError(err)
			clone, err := api.Scheme.DeepCopy(oldCfg)
			framework.ExpectNoError(err)
			newCfg := clone.(*componentconfig.KubeletConfiguration)
			if newCfg.FeatureGates != "" {
				newCfg.FeatureGates = fmt.Sprintf("%s,%s", acceleratorsFeatureGate, newCfg.FeatureGates)
			} else {
				newCfg.FeatureGates = acceleratorsFeatureGate
			}
			framework.ExpectNoError(setKubeletConfiguration(f, newCfg))

			By("Getting the local node object from the api server")
			nodeList, err := f.ClientSet.Core().Nodes().List(metav1.ListOptions{})
			framework.ExpectNoError(err, "getting node list")
			Expect(len(nodeList.Items)).To(Equal(1))
			node := nodeList.Items[0]
			gpusAvailable := node.Status.Capacity.NvidiaGPU()
			By("Skipping the test if GPUs aren't available")
			if gpusAvailable.IsZero() {
				Skip("No GPUs available on local node. Skipping test.")
			}

			By("Creating a pod that will consume all GPUs")
			podSuccess := makePod(gpusAvailable.Value(), "gpus-success")
			podSuccess = f.PodClient().CreateSync(podSuccess)

			By("Checking if the pod outputted Success to its logs")
			framework.ExpectNoError(f.PodClient().MatchContainerOutput(podSuccess.Name, podSuccess.Name, "Success"))

			By("Creating a new pod requesting a GPU and noticing that it is rejected by the Kubelet")
			podFailure := makePod(1, "gpu-failure")
			framework.WaitForPodCondition(f.ClientSet, f.Namespace.Name, podFailure.Name, "pod rejected", framework.PodStartTimeout, func(pod *v1.Pod) (bool, error) {
				if pod.Status.Phase == v1.PodFailed {
					return true, nil

				}
				return false, nil
			})

			By("stopping the original Pod with GPUs")
			gp := int64(0)
			deleteOptions := metav1.DeleteOptions{
				GracePeriodSeconds: &gp,
			}
			f.PodClient().DeleteSync(podSuccess.Name, &deleteOptions, 30*time.Second)

			By("attempting to start the failed pod again")
			f.PodClient().DeleteSync(podFailure.Name, &deleteOptions, 10*time.Second)
			podFailure = f.PodClient().CreateSync(podFailure)

			By("Checking if the pod outputted Success to its logs")
			framework.ExpectNoError(f.PodClient().MatchContainerOutput(podFailure.Name, podFailure.Name, "Success"))
		})
	})
})

func makePod(gpus int64, name string) *v1.Pod {
	resources := v1.ResourceRequirements{
		Limits: v1.ResourceList{
			v1.ResourceNvidiaGPU: *resource.NewQuantity(gpus, resource.DecimalSI),
		},
	}
	gpuverificationCmd := fmt.Sprintf("if [[ %d -ne $(ls /dev/ | egrep '^nvidia[0-9]+$') ]]; then exit 1; fi; echo Success; sleep 10240 ", gpus)
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Image:     "gcr.io/google_containers/busybox:1.24",
					Name:      name,
					Command:   []string{"sh", "-c", gpuverificationCmd},
					Resources: resources,
				},
			},
		},
	}
}
