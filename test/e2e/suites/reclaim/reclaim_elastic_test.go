/*
Copyright 2025 NVIDIA CORPORATION
SPDX-License-Identifier: Apache-2.0
*/
package reclaim

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	v2 "github.com/NVIDIA/KAI-scheduler/pkg/apis/scheduling/v2"
	"github.com/NVIDIA/KAI-scheduler/pkg/common/constants"
	testcontext "github.com/NVIDIA/KAI-scheduler/test/e2e/modules/context"
	"github.com/NVIDIA/KAI-scheduler/test/e2e/modules/resources/capacity"
	"github.com/NVIDIA/KAI-scheduler/test/e2e/modules/resources/rd"
	"github.com/NVIDIA/KAI-scheduler/test/e2e/modules/resources/rd/pod_group"
	"github.com/NVIDIA/KAI-scheduler/test/e2e/modules/resources/rd/queue"
	"github.com/NVIDIA/KAI-scheduler/test/e2e/modules/wait"
)

const (
	podGroupLabelName = "pod-group-name"
)

var _ = Describe("Reclaim with Elastic Jobs", Ordered, func() {
	var (
		testCtx            *testcontext.TestContext
		parentQueue        *v2.Queue
		reclaimeeQueue     *v2.Queue
		reclaimerQueue     *v2.Queue
		reclaimeeNamespace string
	)

	BeforeAll(func(ctx context.Context) {
		testCtx = testcontext.GetConnectivity(ctx, Default)
		capacity.SkipIfInsufficientClusterResources(testCtx.KubeClientset, &capacity.ResourceList{
			Gpu:      resource.MustParse("4"),
			PodCount: 5,
		})
	})

	AfterEach(func(ctx context.Context) {
		testCtx.ClusterCleanup(ctx)
	})

	It("Reclaim tasks from elastic jobs", func(ctx context.Context) {
		testCtx = testcontext.GetConnectivity(ctx, Default)
		parentQueue, reclaimeeQueue, reclaimerQueue = createQueues(4, 2, 2)
		testCtx.InitQueues([]*v2.Queue{parentQueue, reclaimeeQueue, reclaimerQueue})
		reclaimeeNamespace = queue.GetConnectedNamespaceToQueue(reclaimeeQueue)

		reclaimeePodRequirements := v1.ResourceRequirements{
			Limits: map[v1.ResourceName]resource.Quantity{
				constants.GpuResource: resource.MustParse("1"),
			},
		}

		// first reclaimee job
		reclaimeePodgroup1, pods1 := pod_group.CreateWithPods(ctx, testCtx.KubeClientset, testCtx.KubeAiSchedClientset,
			"elastic-reclaimee-job-1", reclaimeeQueue, 2, nil,
			reclaimeePodRequirements)

		// second reclaimee job
		reclaimeePodgroup2, pods2 := pod_group.CreateWithPods(ctx, testCtx.KubeClientset, testCtx.KubeAiSchedClientset,
			"elastic-reclaimee-job-2", reclaimeeQueue, 2, nil,
			reclaimeePodRequirements)

		var preempteePods []*v1.Pod
		preempteePods = append(preempteePods, pods1...)
		preempteePods = append(preempteePods, pods2...)
		wait.ForPodsScheduled(ctx, testCtx.ControllerClient, reclaimeeNamespace, preempteePods)

		// reclaimer job
		reclaimerPodRequirements := v1.ResourceRequirements{
			Limits: map[v1.ResourceName]resource.Quantity{
				constants.GpuResource: resource.MustParse("2"),
			},
		}

		reclaimerPod := rd.CreatePodObject(reclaimerQueue, reclaimerPodRequirements)
		reclaimerPod, err := rd.CreatePod(ctx, testCtx.KubeClientset, reclaimerPod)
		Expect(err).To(Succeed())
		wait.ForPodScheduled(ctx, testCtx.ControllerClient, reclaimerPod)

		// verify results
		wait.ForPodsWithCondition(ctx, testCtx.ControllerClient, func(watch.Event) bool {
			pods, err := testCtx.KubeClientset.CoreV1().Pods(reclaimeeNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("%s=%s", podGroupLabelName, reclaimeePodgroup1.Name),
			})
			Expect(err).To(Succeed())
			return len(pods.Items) == 1
		})

		wait.ForPodsWithCondition(ctx, testCtx.ControllerClient, func(watch.Event) bool {
			pods, err := testCtx.KubeClientset.CoreV1().Pods(reclaimeeNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("%s=%s", podGroupLabelName, reclaimeePodgroup2.Name),
			})
			Expect(err).To(Succeed())
			return len(pods.Items) == 1
		})
	})

	It("Reclaim entire elastic job", func(ctx context.Context) {
		testCtx = testcontext.GetConnectivity(ctx, Default)
		parentQueue, reclaimeeQueue, reclaimerQueue = createQueues(2, 0, 2)
		reclaimeeQueue.Spec.Resources.GPU.OverQuotaWeight = 0
		testCtx.InitQueues([]*v2.Queue{parentQueue, reclaimeeQueue, reclaimerQueue})
		reclaimeeNamespace = queue.GetConnectedNamespaceToQueue(reclaimeeQueue)

		// reclaimee job
		reclaimeePodRequirements := v1.ResourceRequirements{
			Limits: map[v1.ResourceName]resource.Quantity{
				constants.GpuResource: resource.MustParse("1"),
			},
		}
		reclaimeePodGroup, reclaimeePods := pod_group.CreateWithPods(ctx, testCtx.KubeClientset, testCtx.KubeAiSchedClientset,
			"elastic-reclaimee-job", reclaimeeQueue, 2, nil,
			reclaimeePodRequirements)
		wait.ForPodsScheduled(ctx, testCtx.ControllerClient, reclaimeeNamespace, reclaimeePods)

		// reclaimer job
		reclaimerPodRequirements := v1.ResourceRequirements{
			Limits: map[v1.ResourceName]resource.Quantity{
				constants.GpuResource: resource.MustParse("2"),
			},
		}
		reclaimerPod := rd.CreatePodObject(reclaimerQueue, reclaimerPodRequirements)
		reclaimerPod, err := rd.CreatePod(ctx, testCtx.KubeClientset, reclaimerPod)
		Expect(err).To(Succeed())
		wait.ForPodScheduled(ctx, testCtx.ControllerClient, reclaimerPod)

		// verify results
		wait.ForPodsWithCondition(ctx, testCtx.ControllerClient, func(watch.Event) bool {
			pods, err := testCtx.KubeClientset.CoreV1().Pods(reclaimeeNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("%s=%s", podGroupLabelName, reclaimeePodGroup.Name),
			})
			Expect(err).To(Succeed())
			return len(pods.Items) == 0
		})
	})

	It("Reclaim elastic job for a distributed job", func(ctx context.Context) {
		testCtx = testcontext.GetConnectivity(ctx, Default)
		parentQueue, reclaimeeQueue, reclaimerQueue = createQueues(2, 0, 2)
		reclaimeeQueue.Spec.Resources.GPU.OverQuotaWeight = 0
		testCtx.InitQueues([]*v2.Queue{parentQueue, reclaimeeQueue, reclaimerQueue})
		reclaimeeNamespace = queue.GetConnectedNamespaceToQueue(reclaimeeQueue)

		// reclaimee job
		reclaimeePodRequirements := v1.ResourceRequirements{
			Limits: map[v1.ResourceName]resource.Quantity{
				constants.GpuResource: resource.MustParse("1"),
			},
		}
		reclaimeePodGroup, reclaimeePods := pod_group.CreateWithPods(ctx, testCtx.KubeClientset, testCtx.KubeAiSchedClientset,
			"elastic-reclaimee-job", reclaimeeQueue, 2, nil,
			reclaimeePodRequirements)
		wait.ForPodsScheduled(ctx, testCtx.ControllerClient, reclaimeeNamespace, reclaimeePods)

		// reclaimer job
		reclaimerPodRequirements := v1.ResourceRequirements{
			Limits: map[v1.ResourceName]resource.Quantity{
				constants.GpuResource: resource.MustParse("1"),
			},
		}
		_, reclaimerPods := pod_group.CreateDistributedJob(
			ctx, testCtx.KubeClientset, testCtx.ControllerClient,
			reclaimerQueue, 2, reclaimerPodRequirements, "",
		)
		reclaimerNamespace := queue.GetConnectedNamespaceToQueue(reclaimerQueue)
		wait.ForPodsScheduled(ctx, testCtx.ControllerClient, reclaimerNamespace, reclaimerPods)

		// verify results
		wait.ForPodsWithCondition(ctx, testCtx.ControllerClient, func(watch.Event) bool {
			pods, err := testCtx.KubeClientset.CoreV1().Pods(reclaimeeNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("%s=%s", podGroupLabelName, reclaimeePodGroup.Name),
			})
			Expect(err).To(Succeed())
			return len(pods.Items) == 0
		})
	})

	It("Reclaim elastic job partially for a distributed job", func(ctx context.Context) {
		testCtx = testcontext.GetConnectivity(ctx, Default)
		parentQueue, reclaimeeQueue, reclaimerQueue = createQueues(3, 1, 2)
		reclaimeeQueue.Spec.Resources.GPU.OverQuotaWeight = 0
		testCtx.InitQueues([]*v2.Queue{parentQueue, reclaimeeQueue, reclaimerQueue})
		reclaimeeNamespace = queue.GetConnectedNamespaceToQueue(reclaimeeQueue)

		// reclaimee job
		reclaimeePodRequirements := v1.ResourceRequirements{
			Limits: map[v1.ResourceName]resource.Quantity{
				constants.GpuResource: resource.MustParse("1"),
			},
		}
		reclaimeePodGroup, reclaimeePods := pod_group.CreateWithPods(ctx, testCtx.KubeClientset, testCtx.KubeAiSchedClientset,
			"elastic-reclaimee-job", reclaimeeQueue, 3, nil,
			reclaimeePodRequirements)
		wait.ForPodsScheduled(ctx, testCtx.ControllerClient, reclaimeeNamespace, reclaimeePods)

		// reclaimer job
		reclaimerPodRequirements := v1.ResourceRequirements{
			Limits: map[v1.ResourceName]resource.Quantity{
				constants.GpuResource: resource.MustParse("1"),
			},
		}
		_, reclaimerPods := pod_group.CreateDistributedJob(
			ctx, testCtx.KubeClientset, testCtx.ControllerClient,
			reclaimerQueue, 2, reclaimerPodRequirements, "",
		)
		reclaimerNamespace := queue.GetConnectedNamespaceToQueue(reclaimerQueue)
		wait.ForPodsScheduled(ctx, testCtx.ControllerClient, reclaimerNamespace, reclaimerPods)

		// verify results
		wait.ForPodsWithCondition(ctx, testCtx.ControllerClient, func(watch.Event) bool {
			pods, err := testCtx.KubeClientset.CoreV1().Pods(reclaimeeNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("%s=%s", podGroupLabelName, reclaimeePodGroup.Name),
			})
			Expect(err).To(Succeed())
			return len(pods.Items) == 1
		})
	})
})
