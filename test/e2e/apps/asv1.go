// Copyright 2019 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package apps

import (
	"fmt"
	"reflect"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	asv1 "github.com/pingcap/advanced-statefulset/pkg/apis/apps/v1"
	"github.com/pingcap/advanced-statefulset/pkg/apis/apps/v1/helper"
	asclientset "github.com/pingcap/advanced-statefulset/pkg/client/clientset/versioned"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	e2eservice "k8s.io/kubernetes/test/e2e/framework/service"
	e2esset "k8s.io/kubernetes/test/e2e/framework/statefulset"
)

var _ = SIGDescribe("AdvancedStatefulSet[V1]", func() {
	f := framework.NewDefaultFramework("statefulset")
	var ns string
	var c clientset.Interface
	var asc asclientset.Interface
	var hc clientset.Interface

	ginkgo.BeforeEach(func() {
		ns = f.Namespace.Name
		c = f.ClientSet
		config, err := framework.LoadConfig()
		framework.ExpectNoError(err)
		asc, err = asclientset.NewForConfig(config)
		framework.ExpectNoError(err)
		hc = helper.NewHijackClient(f.ClientSet, asc)
	})

	ginkgo.Describe("Basic StatefulSet functionality [StatefulSetBasic]", func() {
		ssName := "ss"
		ssLabels := map[string]string{
			"foo": "bar",
			"baz": "blah",
		}
		headlessSvcName := "test"
		var statefulPodMounts, podMounts []v1.VolumeMount
		var ss *appsv1.StatefulSet

		ginkgo.BeforeEach(func() {
			statefulPodMounts = []v1.VolumeMount{{Name: "datadir", MountPath: "/data/"}}
			podMounts = []v1.VolumeMount{{Name: "home", MountPath: "/home"}}
			ss = e2esset.NewStatefulSet(ssName, ns, headlessSvcName, 2, statefulPodMounts, podMounts, ssLabels)

			ginkgo.By("Creating service " + headlessSvcName + " in namespace " + ns)
			headlessService := e2eservice.CreateServiceSpec(headlessSvcName, "", true, ssLabels)
			_, err := c.CoreV1().Services(ns).Create(headlessService)
			framework.ExpectNoError(err)
		})

		ginkgo.AfterEach(func() {
			if ginkgo.CurrentGinkgoTestDescription().Failed {
				framework.DumpDebugInfo(c, ns)
			}
			framework.Logf("Deleting all advanced statefulset in ns %v", ns)
			e2esset.DeleteAllStatefulSets(hc, ns)
		})

		for _, policy := range []appsv1.PodManagementPolicyType{appsv1.OrderedReadyPodManagement, appsv1.ParallelPodManagement} {
			tmpPolicy := policy
			ginkgo.It(fmt.Sprintf("scale in/out with delete slots [podManagementPolicy=%s]", tmpPolicy), func() {
				ginkgo.By(fmt.Sprintf("Creating statefulset %s in namespace %s with pod management policy %s", ssName, ns, tmpPolicy))
				*(ss.Spec.Replicas) = 3
				ss.Spec.PodManagementPolicy = tmpPolicy

				ginkgo.By(fmt.Sprintf("Creating statefulset %q with %d replicas and delete slots %v", ss.Name, 3, []int{}))
				ss, err := hc.AppsV1().StatefulSets(ns).Create(ss)
				framework.ExpectNoError(err)
				e2esset.WaitForStatusReplicas(hc, ss, 3)

				ginkgo.By(fmt.Sprintf("Scaling statefulset %q to %d replicas with delete slots %v", ss.Name, 2, []int{1}))
				ss, err = e2esset.UpdateStatefulSetWithRetries(hc, ns, ss.Name, func(update *appsv1.StatefulSet) {
					*(update.Spec.Replicas) = 2
					helper.AddDeleteSlots(update, sets.NewInt(1))
				})
				framework.ExpectNoError(err)
				e2esset.WaitForStatusReplicas(hc, ss, 2)

				ginkgo.By(fmt.Sprintf("Scaling statefulset %q to %d replicas with delete slots %v", ss.Name, 10, []int{1, 3}))
				ss, err = e2esset.UpdateStatefulSetWithRetries(hc, ns, ss.Name, func(update *appsv1.StatefulSet) {
					*(update.Spec.Replicas) = 10
					helper.AddDeleteSlots(update, sets.NewInt(1, 3))
				})
				framework.ExpectNoError(err)
				e2esset.WaitForStatusReplicas(hc, ss, 10)
			})
		}

		ginkgo.It("scale up/down with delete slots [updateStrategy=OnDelete]", func() {
			framework.Skipf("skip to test legacy behavior")
		})

		// This is modifed from "should perform canary updates and phased rolling updates of template modifications"
		ginkgo.It("scale up/down with delete slots [updateStrategy=RollingUpdate]", func() {
			ginkgo.By("Creating a new StatefulSet")
			ginkgo.By("Creating statefulset " + ssName + " in namespace " + ns)
			*(ss.Spec.Replicas) = 3
			e2esset.SetHTTPProbe(ss)
			deleteSlots := sets.NewInt(1)
			helper.AddDeleteSlots(ss, deleteSlots)
			ss.Spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: func() *appsv1.RollingUpdateStatefulSetStrategy {
					return &appsv1.RollingUpdateStatefulSetStrategy{
						Partition: func() *int32 {
							// expected slots: [0, 2, 3]
							i := int32(4)
							return &i
						}()}
				}(),
			}
			ginkgo.By(fmt.Sprintf("Creating statefulset %q with %d replicas and delete slots %v", ss.Name, 3, deleteSlots.List()))
			ss, err := hc.AppsV1().StatefulSets(ns).Create(ss)
			framework.ExpectNoError(err)
			e2esset.WaitForStatusReplicas(hc, ss, 3) // partition does not apply newly created pods

			ss = e2esset.WaitForStatus(hc, ss)
			currentRevision, updateRevision := ss.Status.CurrentRevision, ss.Status.UpdateRevision
			framework.ExpectEqual(currentRevision, updateRevision, fmt.Sprintf("StatefulSet %s/%s created with update revision %s not equal to current revision %s",
				ss.Namespace, ss.Name, updateRevision, currentRevision))
			pods := e2esset.GetPodList(hc, ss)
			for i := range pods.Items {
				framework.ExpectEqual(pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel], currentRevision, fmt.Sprintf("Pod %s/%s revision %s is not equal to currentRevision %s",
					pods.Items[i].Namespace,
					pods.Items[i].Name,
					pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel],
					currentRevision))
			}

			newImage := NewWebserverImage
			oldImage := ss.Spec.Template.Spec.Containers[0].Image

			ginkgo.By(fmt.Sprintf("Updating stateful set template: update image from %s to %s", oldImage, newImage))
			framework.ExpectNotEqual(oldImage, newImage, "Incorrect test setup: should update to a different image")
			ss, err = e2esset.UpdateStatefulSetWithRetries(hc, ns, ss.Name, func(update *appsv1.StatefulSet) {
				update.Spec.Template.Spec.Containers[0].Image = newImage
			})
			framework.ExpectNoError(err)

			ginkgo.By("Creating a new revision")
			ss = e2esset.WaitForStatus(hc, ss)
			currentRevision, updateRevision = ss.Status.CurrentRevision, ss.Status.UpdateRevision
			framework.ExpectNotEqual(currentRevision, updateRevision, "Current revision should not equal update revision during rolling update")

			ginkgo.By("Not applying an update when the partition is greater than the number of replicas")
			for i := range pods.Items {
				framework.ExpectEqual(pods.Items[i].Spec.Containers[0].Image, oldImage, fmt.Sprintf("Pod %s/%s has image %s not equal to current image %s",
					pods.Items[i].Namespace,
					pods.Items[i].Name,
					pods.Items[i].Spec.Containers[0].Image,
					oldImage))
				framework.ExpectEqual(pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel], currentRevision, fmt.Sprintf("Pod %s/%s has revision %s not equal to current revision %s",
					pods.Items[i].Namespace,
					pods.Items[i].Name,
					pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel],
					currentRevision))
			}

			ginkgo.By("Performing a canary update")
			ss, err = e2esset.UpdateStatefulSetWithRetries(hc, ns, ss.Name, func(update *appsv1.StatefulSet) {
				update.Spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{
					Type: appsv1.RollingUpdateStatefulSetStrategyType,
					RollingUpdate: func() *appsv1.RollingUpdateStatefulSetStrategy {
						return &appsv1.RollingUpdateStatefulSetStrategy{
							Partition: func() *int32 {
								i := int32(3)
								return &i
							}()}
					}(),
				}
			})
			framework.ExpectNoError(err)
			ss, pods = WaitForPartitionedRollingUpdate(hc, ss)
			for i := range pods.Items {
				if getStatefulPodOrdinal(&pods.Items[i]) < int(*ss.Spec.UpdateStrategy.RollingUpdate.Partition) {
					framework.ExpectEqual(pods.Items[i].Spec.Containers[0].Image, oldImage, fmt.Sprintf("Pod %s/%s has image %s not equal to current image %s",
						pods.Items[i].Namespace,
						pods.Items[i].Name,
						pods.Items[i].Spec.Containers[0].Image,
						oldImage))
					framework.ExpectEqual(pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel], currentRevision, fmt.Sprintf("Pod %s/%s has revision %s not equal to current revision %s",
						pods.Items[i].Namespace,
						pods.Items[i].Name,
						pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel],
						currentRevision))
				} else {
					framework.ExpectEqual(pods.Items[i].Spec.Containers[0].Image, newImage, fmt.Sprintf("Pod %s/%s has image %s not equal to new image  %s",
						pods.Items[i].Namespace,
						pods.Items[i].Name,
						pods.Items[i].Spec.Containers[0].Image,
						newImage))
					framework.ExpectEqual(pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel], updateRevision, fmt.Sprintf("Pod %s/%s has revision %s not equal to new revision %s",
						pods.Items[i].Namespace,
						pods.Items[i].Name,
						pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel],
						updateRevision))
				}
			}

			ginkgo.By("Restoring Pods to the correct revision when they are deleted")
			e2esset.DeleteStatefulPodAtIndex(hc, 0, ss)
			e2esset.DeleteStatefulPodAtIndex(hc, 2, ss)
			WaitForRunningAndReady(hc, 3, ss)
			ss = e2esset.GetStatefulSet(hc, ss.Namespace, ss.Name)
			pods = e2esset.GetPodList(hc, ss)
			for i := range pods.Items {
				if getStatefulPodOrdinal(&pods.Items[i]) < int(*ss.Spec.UpdateStrategy.RollingUpdate.Partition) {
					framework.ExpectEqual(pods.Items[i].Spec.Containers[0].Image, oldImage, fmt.Sprintf("Pod %s/%s has image %s not equal to current image %s",
						pods.Items[i].Namespace,
						pods.Items[i].Name,
						pods.Items[i].Spec.Containers[0].Image,
						oldImage))
					framework.ExpectEqual(pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel], currentRevision, fmt.Sprintf("Pod %s/%s has revision %s not equal to current revision %s",
						pods.Items[i].Namespace,
						pods.Items[i].Name,
						pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel],
						currentRevision))
				} else {
					framework.ExpectEqual(pods.Items[i].Spec.Containers[0].Image, newImage, fmt.Sprintf("Pod %s/%s has image %s not equal to new image  %s",
						pods.Items[i].Namespace,
						pods.Items[i].Name,
						pods.Items[i].Spec.Containers[0].Image,
						newImage))
					framework.ExpectEqual(pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel], updateRevision, fmt.Sprintf("Pod %s/%s has revision %s not equal to new revision %s",
						pods.Items[i].Namespace,
						pods.Items[i].Name,
						pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel],
						updateRevision))
				}
			}

			ginkgo.By("Performing a phased rolling update")
			for i := int(*ss.Spec.UpdateStrategy.RollingUpdate.Partition) - 1; i >= 0; i-- {
				ss, err = e2esset.UpdateStatefulSetWithRetries(hc, ns, ss.Name, func(update *appsv1.StatefulSet) {
					update.Spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{
						Type: appsv1.RollingUpdateStatefulSetStrategyType,
						RollingUpdate: func() *appsv1.RollingUpdateStatefulSetStrategy {
							j := int32(i)
							return &appsv1.RollingUpdateStatefulSetStrategy{
								Partition: &j,
							}
						}(),
					}
				})
				framework.ExpectNoError(err)
				ss, pods = WaitForPartitionedRollingUpdate(hc, ss)
				for i := range pods.Items {
					if getStatefulPodOrdinal(&pods.Items[i]) < int(*ss.Spec.UpdateStrategy.RollingUpdate.Partition) {
						framework.ExpectEqual(pods.Items[i].Spec.Containers[0].Image, oldImage, fmt.Sprintf("Pod %s/%s has image %s not equal to current image %s",
							pods.Items[i].Namespace,
							pods.Items[i].Name,
							pods.Items[i].Spec.Containers[0].Image,
							oldImage))
						framework.ExpectEqual(pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel], currentRevision, fmt.Sprintf("Pod %s/%s has revision %s not equal to current revision %s",
							pods.Items[i].Namespace,
							pods.Items[i].Name,
							pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel],
							currentRevision))
					} else {
						framework.ExpectEqual(pods.Items[i].Spec.Containers[0].Image, newImage, fmt.Sprintf("Pod %s/%s has image %s not equal to new image  %s",
							pods.Items[i].Namespace,
							pods.Items[i].Name,
							pods.Items[i].Spec.Containers[0].Image,
							newImage))
						framework.ExpectEqual(pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel], updateRevision, fmt.Sprintf("Pod %s/%s has revision %s not equal to new revision %s",
							pods.Items[i].Namespace,
							pods.Items[i].Name,
							pods.Items[i].Labels[appsv1.StatefulSetRevisionLabel],
							updateRevision))
					}
				}
			}

			framework.ExpectEqual(ss.Status.CurrentRevision, updateRevision, fmt.Sprintf("StatefulSet %s/%s current revision %s does not equal update revision %s on update completion",
				ss.Namespace,
				ss.Name,
				ss.Status.CurrentRevision,
				updateRevision))
		})

		ginkgo.It("migrate from Kubernetes StatefulSet", func() {
			ginkgo.By("Creating statefulset " + ssName + " in namespace " + ns)
			*(ss.Spec.Replicas) = 3
			e2esset.PauseNewPods(ss)

			ss, err := c.AppsV1().StatefulSets(ns).Create(ss)
			framework.ExpectNoError(err)

			ginkgo.By("Saturating stateful set " + ss.Name)
			e2esset.Saturate(c, ss)

			ginkgo.By(fmt.Sprintf("Orphan and save controller revisions"))
			selector, err := metav1.LabelSelectorAsSelector(ss.Spec.Selector)
			framework.ExpectNoError(err)
			revisionListOptions := metav1.ListOptions{LabelSelector: selector.String()}
			oldRevisionList, err := c.AppsV1().ControllerRevisions(ns).List(revisionListOptions)
			framework.ExpectNoError(err)
			migrateLabel := "apps.pingcap.com/migrate"
			for _, revision := range oldRevisionList.Items {
				// It's important to empty statefulset selector labels,
				// otherwise sts will adopt it again on delete event and then
				// GC will delete revisions because they are not orphans.
				// https://github.com/kubernetes/kubernetes/issues/84982
				for key := range ss.Spec.Selector.MatchLabels {
					delete(revision.Labels, key)
				}
				revision.Labels[migrateLabel] = ss.Name // a special label to mark it should be adopted by the new sts
				_, err = c.AppsV1().ControllerRevisions(ns).Update(&revision)
				framework.ExpectNoError(err)
			}

			ginkgo.By(fmt.Sprintf("Deleting stateful set %q without dependents", ss.Name))
			policy := metav1.DeletePropagationOrphan
			c.AppsV1().StatefulSets(ns).Delete(ss.Name, &metav1.DeleteOptions{
				PropagationPolicy: &policy,
			})

			migrateSelector := labels.SelectorFromSet(map[string]string{
				migrateLabel: ss.Name,
			})
			migrateRevisionListOption := metav1.ListOptions{LabelSelector: migrateSelector.String()}

			ginkgo.By("Waiting for pods/controllerrevisions to be orphaned")
			err = wait.PollImmediate(time.Second, time.Minute, func() (done bool, err error) {
				podList := e2esset.GetPodList(c, ss)
				gomega.Expect(podList.Items).To(gomega.HaveLen(int(*ss.Spec.Replicas)))
				for _, pod := range podList.Items {
					if metav1.GetControllerOf(&pod) != nil {
						return false, nil
					}
				}
				revisionList, err := c.AppsV1().ControllerRevisions(ns).List(migrateRevisionListOption)
				framework.ExpectNoError(err)
				gomega.Expect(revisionList.Items).To(gomega.HaveLen(len(oldRevisionList.Items)))

				for _, revision := range revisionList.Items {
					if metav1.GetControllerOf(&revision) != nil {
						return false, nil
					}
				}
				return true, nil
			})
			framework.ExpectNoError(err)

			ginkgo.By("Wait for a while that pods/controllerrevisions should not be deleted")
			err = wait.PollImmediate(time.Second, time.Minute, func() (done bool, err error) {
				podList := e2esset.GetPodList(c, ss)
				gomega.Expect(podList.Items).To(gomega.HaveLen(int(*ss.Spec.Replicas)))
				for _, pod := range podList.Items {
					gomega.Expect(pod.Status.Phase).To(gomega.Equal(v1.PodRunning))
					gomega.Expect(metav1.GetControllerOf(&pod)).To(gomega.BeNil())
				}

				revisionList, err := c.AppsV1().ControllerRevisions(ns).List(migrateRevisionListOption)
				framework.ExpectNoError(err)
				gomega.Expect(revisionList.Items).To(gomega.HaveLen(len(oldRevisionList.Items)))
				for _, revision := range revisionList.Items {
					gomega.Expect(metav1.GetControllerOf(&revision)).To(gomega.BeNil())
				}
				return false, nil
			})
			framework.ExpectEqual(err, wait.ErrWaitTimeout)

			ginkgo.By(fmt.Sprintf("Creating a new advanced sts %q", ss.Name))
			asts, err := helper.FromBuiltinStatefulSet(ss)
			framework.ExpectNoError(err)
			asts.ObjectMeta = metav1.ObjectMeta{
				Name:      ss.Name,
				Namespace: ss.Namespace,
			}
			asts.Status = asv1.StatefulSetStatus{}
			asts, err = asc.AppsV1().StatefulSets(ns).Create(asts)
			framework.ExpectNoError(err)

			ginkgo.By("Recovering controller revisions")
			revisionList, err := c.AppsV1().ControllerRevisions(ns).List(migrateRevisionListOption)
			framework.ExpectNoError(err)
			for _, revision := range revisionList.Items {
				delete(revision.Labels, migrateLabel)
				for key, val := range asts.Spec.Selector.MatchLabels {
					revision.Labels[key] = val
				}
				_, err = c.AppsV1().ControllerRevisions(ns).Update(&revision)
				framework.ExpectNoError(err)
			}

			ginkgo.By(fmt.Sprintf("Wait for pods/controllerrevisions to be adopted"))
			controllerKind := asv1.SchemeGroupVersion.WithKind("StatefulSet")
			controllerRef := metav1.NewControllerRef(asts, controllerKind)
			err = wait.PollImmediate(time.Second, time.Minute, func() (done bool, err error) {
				podList := e2esset.GetPodList(c, ss)
				gomega.Expect(podList.Items).To(gomega.HaveLen(int(*ss.Spec.Replicas)))
				for _, pod := range podList.Items {
					ref := metav1.GetControllerOf(&pod)
					if ref == nil {
						return false, nil
					}
					if !reflect.DeepEqual(ref, controllerRef) {
						return false, nil
					}
				}
				revisionList, err := c.AppsV1().ControllerRevisions(ns).List(revisionListOptions)
				framework.ExpectNoError(err)
				gomega.Expect(revisionList.Items).To(gomega.HaveLen(len(oldRevisionList.Items)))

				for _, revision := range revisionList.Items {
					ref := metav1.GetControllerOf(&revision)
					if ref == nil {
						return false, nil
					}
					if !reflect.DeepEqual(ref, controllerRef) {
						return false, nil
					}
				}
				return true, nil
			})
			framework.ExpectNoError(err)
		})
	})

})