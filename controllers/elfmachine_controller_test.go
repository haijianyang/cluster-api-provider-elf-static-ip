/*
Copyright 2022.

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

package controllers

import (
	"bytes"
	goctx "context"
	"flag"
	"fmt"
	"time"

	ipamv1 "github.com/metal3-io/ip-address-manager/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	capev1 "github.com/smartxworks/cluster-api-provider-elf/api/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capiutil "sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/smartxworks/cluster-api-provider-elf-static-ip/pkg/config"
	"github.com/smartxworks/cluster-api-provider-elf-static-ip/pkg/ipam"
	ipamutil "github.com/smartxworks/cluster-api-provider-elf-static-ip/pkg/ipam/util"
	"github.com/smartxworks/cluster-api-provider-elf-static-ip/test/fake"
)

var _ = Describe("ElfMachineReconciler", func() {
	var (
		logBuffer          *bytes.Buffer
		elfCluster         *capev1.ElfCluster
		cluster            *capiv1.Cluster
		elfMachine         *capev1.ElfMachine
		machine            *capiv1.Machine
		elfMachineTemplate *capev1.ElfMachineTemplate
		metal3IPPool       *ipamv1.IPPool
		metal3IPClaim      *ipamv1.IPClaim
		metal3IPAddress    *ipamv1.IPAddress
	)

	ctx := goctx.Background()

	BeforeEach(func() {
		// set log
		if err := flag.Set("logtostderr", "false"); err != nil {
			_ = fmt.Errorf("Error setting logtostderr flag")
		}
		if err := flag.Set("v", "6"); err != nil {
			_ = fmt.Errorf("Error setting v flag")
		}
		klog.SetOutput(GinkgoWriter)
		logBuffer = new(bytes.Buffer)

		elfCluster, cluster, elfMachine, machine, elfMachineTemplate = fake.NewClusterAndMachineObjects()
		metal3IPPool = fake.NewMetal3IPPool()
	})

	It("should not reconcile when ElfMachine not found", func() {
		klog.SetOutput(logBuffer)
		ctrlContext := newCtrlContexts()

		reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
		Expect(result).To(BeZero())
		Expect(err).To(BeNil())
		Expect(logBuffer.String()).To(ContainSubstring("ElfMachine not found, won't reconcile"))
	})

	It("should not reconcile when ElfMachine in an error state", func() {
		klog.SetOutput(logBuffer)
		elfMachine.Status.FailureMessage = pointer.StringPtr("some error")
		ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate)
		fake.InitOwnerReferences(ctrlContext, elfCluster, cluster, elfMachine, machine)

		reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
		Expect(result).To(BeZero())
		Expect(err).To(BeNil())
		Expect(logBuffer.String()).To(ContainSubstring("Error state detected, skipping reconciliation"))
	})

	It("should not reconcile when ElfMachine without Machine", func() {
		klog.SetOutput(logBuffer)
		ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate)

		reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
		Expect(result).To(BeZero())
		Expect(err).To(BeNil())
		Expect(logBuffer.String()).To(ContainSubstring("Waiting for Machine Controller to set OwnerRef on ElfMachine"))
	})

	It("should not reconcile when ElfMachine without Machine", func() {
		klog.SetOutput(logBuffer)
		cluster.Spec.Paused = true
		ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate)
		fake.InitOwnerReferences(ctrlContext, elfCluster, cluster, elfMachine, machine)

		reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
		Expect(result).To(BeZero())
		Expect(err).To(BeNil())
		Expect(logBuffer.String()).To(ContainSubstring("ElfMachine linked to a cluster that is paused"))
	})

	It("should not reconcile without devices", func() {
		klog.SetOutput(logBuffer)
		elfMachine.Spec.Network.Devices = []capev1.NetworkDeviceSpec{}
		ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate)
		fake.InitOwnerReferences(ctrlContext, elfCluster, cluster, elfMachine, machine)

		reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
		Expect(result).To(BeZero())
		Expect(err).To(BeNil())
		Expect(logBuffer.String()).To(ContainSubstring("no network device found"))
		Expect(ctrlContext.Client.Get(ctrlContext, capiutil.ObjectKey(elfMachine), elfMachine)).To(Succeed())
		Expect(ctrlutil.ContainsFinalizer(elfMachine, MachineIPFinalizer)).To(BeFalse())
	})

	It("should not reconcile when no need to allocate IP", func() {
		klog.SetOutput(logBuffer)
		elfMachine.Spec.Network.Devices = []capev1.NetworkDeviceSpec{
			{NetworkType: capev1.NetworkTypeIPV4, IPAddrs: []string{fake.IP()}},
			{NetworkType: capev1.NetworkTypeIPV4DHCP},
			{NetworkType: capev1.NetworkTypeNone},
		}
		ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate)
		fake.InitOwnerReferences(ctrlContext, elfCluster, cluster, elfMachine, machine)

		reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
		Expect(result).To(BeZero())
		Expect(err).To(BeNil())
		Expect(logBuffer.String()).To(ContainSubstring("no need to allocate IP"))
		Expect(ctrlContext.Client.Get(ctrlContext, capiutil.ObjectKey(elfMachine), elfMachine)).To(Succeed())
		Expect(ctrlutil.ContainsFinalizer(elfMachine, MachineIPFinalizer)).To(BeFalse())
	})

	It("should not reconcile when no cloned-from-name annotation", func() {
		klog.SetOutput(logBuffer)
		elfMachine.Annotations[capiv1.TemplateClonedFromNameAnnotation] = ""
		ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate)
		fake.InitOwnerReferences(ctrlContext, elfCluster, cluster, elfMachine, machine)

		reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
		Expect(result).To(BeZero())
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		Expect(logBuffer.String()).To(ContainSubstring("failed to get IPPool match labels"))
		Expect(ctrlContext.Client.Get(ctrlContext, capiutil.ObjectKey(elfMachine), elfMachine)).To(Succeed())
		Expect(ctrlutil.ContainsFinalizer(elfMachine, MachineIPFinalizer)).To(BeTrue())
	})

	It("should not reconcile when no IPPool", func() {
		klog.SetOutput(logBuffer)
		ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate)
		fake.InitOwnerReferences(ctrlContext, elfCluster, cluster, elfMachine, machine)

		reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
		Expect(result).To(BeZero())
		Expect(err).To(BeNil())
		Expect(logBuffer.String()).To(ContainSubstring("waiting for IPPool to be available"))
		Expect(ctrlContext.Client.Get(ctrlContext, capiutil.ObjectKey(elfMachine), elfMachine)).To(Succeed())
		Expect(ctrlutil.ContainsFinalizer(elfMachine, MachineIPFinalizer)).To(BeTrue())
	})

	It("should create IPClaim and wait when no IPClaim", func() {
		klog.SetOutput(logBuffer)
		elfMachineTemplate.Labels[ipam.ClusterIPPoolNameKey] = metal3IPPool.Name
		ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate, metal3IPPool)
		fake.InitOwnerReferences(ctrlContext, elfCluster, cluster, elfMachine, machine)

		reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
		Expect(result.RequeueAfter).To(Equal(config.DefaultRequeue))
		Expect(err).To(BeNil())
		Expect(logBuffer.String()).To(ContainSubstring(fmt.Sprintf("waiting for IP address %s to be available", ipamutil.GetFormattedClaimName(elfMachine.Name, 0))))
		var ipClaim ipamv1.IPClaim
		Expect(ctrlContext.Client.Get(ctrlContext, apitypes.NamespacedName{
			Namespace: metal3IPPool.Namespace,
			Name:      ipamutil.GetFormattedClaimName(elfMachine.Name, 0),
		}, &ipClaim)).To(Succeed())
		Expect(ipClaim.Spec.Pool.Name).To(Equal(metal3IPPool.Name))
		Expect(ctrlContext.Client.Get(ctrlContext, capiutil.ObjectKey(elfMachine), elfMachine)).To(Succeed())
		Expect(ctrlutil.ContainsFinalizer(elfMachine, MachineIPFinalizer)).To(BeTrue())
	})

	It("should wait for IP when IPClaim without IP", func() {
		klog.SetOutput(logBuffer)
		metal3IPPool.Labels = map[string]string{
			ipam.ClusterIPPoolGroupKey: "ip-pool-group",
			ipam.ClusterNetworkNameKey: "ip-pool-vm-network",
		}
		elfMachineTemplate.Labels = metal3IPPool.Labels
		metal3IPClaim, metal3IPAddress = fake.NewMetal3IPObjects(metal3IPPool, ipamutil.GetFormattedClaimName(elfMachine.Name, 0))
		ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate, metal3IPPool, metal3IPClaim)
		fake.InitOwnerReferences(ctrlContext, elfCluster, cluster, elfMachine, machine)

		reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
		Expect(result.RequeueAfter).To(Equal(config.DefaultRequeue))
		Expect(err).To(BeNil())
		Expect(logBuffer.String()).To(ContainSubstring(fmt.Sprintf("IPClaim %s already exists, skipping creation", ipamutil.GetFormattedClaimName(elfMachine.Name, 0))))
		Expect(logBuffer.String()).To(ContainSubstring(fmt.Sprintf("waiting for IP address %s to be available", ipamutil.GetFormattedClaimName(elfMachine.Name, 0))))
		Expect(ctrlContext.Client.Get(ctrlContext, capiutil.ObjectKey(elfMachine), elfMachine)).To(Succeed())
		Expect(ctrlutil.ContainsFinalizer(elfMachine, MachineIPFinalizer)).To(BeTrue())
	})

	It("should set IP for devices when IP ready", func() {
		klog.SetOutput(logBuffer)
		metal3IPPool.Namespace = ipam.DefaultIPPoolNamespace
		metal3IPPool.Labels = map[string]string{ipam.DefaultIPPoolKey: ""}
		metal3IPClaim, metal3IPAddress = fake.NewMetal3IPObjects(metal3IPPool, ipamutil.GetFormattedClaimName(elfMachine.Name, 0))
		setMetal3IPForClaim(metal3IPClaim, metal3IPAddress)
		ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate, metal3IPPool, metal3IPClaim, metal3IPAddress)
		fake.InitOwnerReferences(ctrlContext, elfCluster, cluster, elfMachine, machine)

		reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
		Expect(result).To(BeZero())
		Expect(err).To(BeNil())
		Expect(logBuffer.String()).To(ContainSubstring("Set IP address successfully"))
		Expect(ctrlContext.Client.Get(ctrlContext, capiutil.ObjectKey(elfMachine), elfMachine)).To(Succeed())
		Expect(elfMachine.Spec.Network.Devices[0].IPAddrs).To(Equal([]string{string(metal3IPAddress.Spec.Address)}))
		Expect(ctrlutil.ContainsFinalizer(elfMachine, MachineIPFinalizer)).To(BeTrue())
	})

	Context("Delete a ElfMachine", func() {
		BeforeEach(func() {
			ctrlutil.AddFinalizer(elfMachine, capiv1.MachineFinalizer)
			ctrlutil.AddFinalizer(elfMachine, MachineIPFinalizer)
			elfMachine.DeletionTimestamp = &metav1.Time{Time: time.Now().UTC()}
		})

		It("should delete normally after MachineFinalizer was removed", func() {
			ctrlutil.RemoveFinalizer(elfMachine, capiv1.MachineFinalizer)
			elfMachine.Spec.Network.Devices = nil
			ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate)
			fake.InitOwnerReferences(ctrlContext, elfCluster, cluster, elfMachine, machine)

			reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
			Expect(result).To(BeZero())
			Expect(err).To(BeNil())
			Expect(apierrors.IsNotFound(ctrlContext.Client.Get(ctrlContext, capiutil.ObjectKey(elfMachine), elfMachine))).To(BeTrue())
		})

		It("should remove MachineIPFinalizer without devices", func() {
			elfMachine.Spec.Network.Devices = nil
			ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate)
			fake.InitOwnerReferences(ctrlContext, elfCluster, cluster, elfMachine, machine)

			reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
			Expect(result).To(BeZero())
			Expect(err).To(BeNil())
			Expect(ctrlContext.Client.Get(ctrlContext, capiutil.ObjectKey(elfMachine), elfMachine)).To(Succeed())
			Expect(ctrlutil.ContainsFinalizer(elfMachine, MachineIPFinalizer)).To(BeFalse())
		})

		It("should remove MachineIPFinalizer and delete related IPs", func() {
			metal3IPPool.Namespace = ipam.DefaultIPPoolNamespace
			metal3IPPool.Labels = map[string]string{ipam.DefaultIPPoolKey: ""}
			metal3IPClaim, metal3IPAddress = fake.NewMetal3IPObjects(metal3IPPool, ipamutil.GetFormattedClaimName(elfMachine.Name, 0))
			setMetal3IPForClaim(metal3IPClaim, metal3IPAddress)
			ctrlContext := newCtrlContexts(elfCluster, cluster, elfMachine, machine, elfMachineTemplate, metal3IPPool, metal3IPClaim, metal3IPAddress)
			fake.InitOwnerReferences(ctrlContext, elfCluster, cluster, elfMachine, machine)

			reconciler := &ElfMachineReconciler{ControllerContext: ctrlContext}
			result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: capiutil.ObjectKey(elfMachine)})
			Expect(result).To(BeZero())
			Expect(err).To(BeNil())
			Expect(ctrlContext.Client.Get(ctrlContext, capiutil.ObjectKey(elfMachine), elfMachine)).To(Succeed())
			Expect(ctrlutil.ContainsFinalizer(elfMachine, MachineIPFinalizer)).To(BeFalse())
			Expect(apierrors.IsNotFound(ctrlContext.Client.Get(ctrlContext, capiutil.ObjectKey(metal3IPClaim), metal3IPClaim))).To(BeTrue())
		})
	})
})
