package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	opwebhook "github.com/ContentRemixer/Contentremixer/app/internal/webhook"
	// +kubebuilder:scaffold:imports
)

// These tests use envtest (a local kube-apiserver + etcd) to prove:
//   - the generated CRDs install,
//   - the controllers reconcile (render + secret resolution + status),
//   - the validating webhooks enforce the design §2.1a/§2.2a rules,
//   - OpenAPI validation (enums, patterns, MinItems) rejects bad specs.
var (
	cfg       *rest.Config
	testEnv   *envtest.Environment
	ctx       context.Context
	cancelCtx context.CancelFunc
	k8sClient client.Client
	ns        = "contentremixer-test"
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	// The shared k8sClient reads through the manager cache; read-after-write
	// lag and reconcile settling need more than the 1s default.
	SetDefaultEventuallyTimeout(10 * time.Second)
	SetDefaultEventuallyPollingInterval(100 * time.Millisecond)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancelCtx = context.WithCancel(context.TODO())

	var err error
	testEnv = &envtest.Environment{
		CRDInstallOptions: envtest.CRDInstallOptions{
			ErrorIfPathMissing: true,
		},
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "config", "webhook")},
		},
	}
	testEnv.CRDDirectoryPaths = []string{filepath.Join("..", "..", "config", "crd", "bases")}

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	utilruntimeMust(contentremixerv1alpha1.AddToScheme(scheme.Scheme))
	// +kubebuilder:scaffold:scheme

	// Start the manager in-process with both controllers + both validating
	// webhooks, pointed at the envtest webhook server.
	webhookInstallOptions := &testEnv.WebhookInstallOptions
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    webhookInstallOptions.LocalServingHost,
			Port:    webhookInstallOptions.LocalServingPort,
			CertDir: webhookInstallOptions.LocalServingCertDir,
		}),
		LeaderElection: false,
	})
	Expect(err).NotTo(HaveOccurred())

	err = (&ModelProfileReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&ReferenceRepoReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&opwebhook.ModelProfileCustomValidator{Client: mgr.GetClient()}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	err = (&opwebhook.ReferenceRepoCustomValidator{}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	k8sClient = mgr.GetClient()
	testNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
	Expect(k8sClient.Create(ctx, testNS)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).NotTo(HaveOccurred())
	}()
})

var _ = AfterSuite(func() {
	cancelCtx()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

func utilruntimeMust(err error) {
	if err != nil {
		Fail(fmt.Sprintf("failed to add scheme: %v", err))
	}
}
