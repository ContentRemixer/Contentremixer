package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

func referenceRepo(name string) *contentremixerv1alpha1.ReferenceRepo {
	return &contentremixerv1alpha1.ReferenceRepo{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: contentremixerv1alpha1.ReferenceRepoSpec{
			Repo:             "ContentRemixer/Contentremixer",
			TokenRef:         contentremixerv1alpha1.SecretKeyRef{Name: "github-pat", Key: "token"},
			WebhookSecretRef: contentremixerv1alpha1.SecretKeyRef{Name: "github-webhook", Key: "hmac"},
			LabelRoutes:      starterRoutes(),
		},
	}
}

func starterRoutes() []contentremixerv1alpha1.LabelRoute {
	return []contentremixerv1alpha1.LabelRoute{
		{Label: "recycle:blog", InputFormat: "auto", OutputFormat: "blog"},
		{Label: "recycle:short", InputFormat: "auto", OutputFormat: "short"},
		{Label: "recycle:tutorial", InputFormat: "auto", OutputFormat: "tutorial"},
		{Label: "recycle:teleprompter", InputFormat: "auto", OutputFormat: "teleprompter"},
	}
}

var _ = Describe("ReferenceRepo controller + webhook (envtest)", func() {
	It("resolves the PAT Secret, validates routes and sets Ready + RouteCount", func() {
		// Secrets must exist in the CR namespace.
		pat := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "github-pat", Namespace: ns},
			Data:       map[string][]byte{"token": []byte("ghp_test")},
		}
		hook := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "github-webhook", Namespace: ns},
			Data:       map[string][]byte{"hmac": []byte("hooksecret")},
		}
		Expect(k8sClient.Create(ctx, pat)).To(Succeed())
		Expect(k8sClient.Create(ctx, hook)).To(Succeed())

		rr := referenceRepo("crate")
		Expect(k8sClient.Create(ctx, rr)).To(Succeed())

		got := &contentremixerv1alpha1.ReferenceRepo{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "crate"}, got)).To(Succeed())
			g.Expect(got.Status.TokenResolved).To(BeTrue())
			g.Expect(got.Status.RouteCount).To(Equal(4))
			ready := meta.FindStatusCondition(got.Status.Conditions, "Ready")
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			token := meta.FindStatusCondition(got.Status.Conditions, "TokenResolved")
			g.Expect(token).NotTo(BeNil())
			g.Expect(token.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())

		// owner + labelRoutes fields survive round-trip (acceptance).
		Expect(got.Spec.Owner.Subject).To(BeEmpty())
		Expect(got.Spec.LabelRoutes).To(HaveLen(4))
		Expect(got.Spec.LabelRoutes[0].Label).To(Equal("recycle:blog"))
		Expect(got.Spec.LabelRoutes[0].InputFormat).To(Equal("auto"))
		Expect(got.Spec.LabelRoutes[0].OutputFormat).To(Equal("blog"))
	})

	It("fails closed (Ready=False, TokenResolved=False) when the PAT Secret is missing", func() {
		rr := referenceRepo("crate-no-secret")
		// Unique Secret names that do NOT exist (the shared github-pat /
		// github-webhook Secrets from the first spec do exist).
		rr.Spec.TokenRef = contentremixerv1alpha1.SecretKeyRef{Name: "missing-pat", Key: "token"}
		rr.Spec.WebhookSecretRef = contentremixerv1alpha1.SecretKeyRef{Name: "missing-webhook", Key: "hmac"}
		Expect(k8sClient.Create(ctx, rr)).To(Succeed())

		got := &contentremixerv1alpha1.ReferenceRepo{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "crate-no-secret"}, got)).To(Succeed())
			g.Expect(got.Status.TokenResolved).To(BeFalse())
			ready := meta.FindStatusCondition(got.Status.Conditions, "Ready")
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		}).Should(Succeed())
	})

	It("recovers once the missing Secret appears", func() {
		pat := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "late-pat", Namespace: ns},
			Data:       map[string][]byte{"token": []byte("ghp_late")},
		}
		hook := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "late-webhook", Namespace: ns},
			Data:       map[string][]byte{"hmac": []byte("hooksecret")},
		}
		rr := referenceRepo("crate-late")
		rr.Spec.TokenRef.Name = "late-pat"
		rr.Spec.WebhookSecretRef.Name = "late-webhook"
		Expect(k8sClient.Create(ctx, rr)).To(Succeed())

		// Not ready yet.
		got := &contentremixerv1alpha1.ReferenceRepo{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "crate-late"}, got)).To(Succeed())
			g.Expect(got.Status.TokenResolved).To(BeFalse())
		}).Should(Succeed())

		Expect(k8sClient.Create(ctx, pat)).To(Succeed())
		Expect(k8sClient.Create(ctx, hook)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "crate-late"}, got)).To(Succeed())
			g.Expect(got.Status.TokenResolved).To(BeTrue())
			ready := meta.FindStatusCondition(got.Status.Conditions, "Ready")
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
	})

	It("rejects duplicate labels in labelRoutes via the validating webhook", func() {
		rr := referenceRepo("crate-dup")
		rr.Spec.LabelRoutes = append(rr.Spec.LabelRoutes,
			contentremixerv1alpha1.LabelRoute{Label: "recycle:blog", InputFormat: "auto", OutputFormat: "blog"})
		err := k8sClient.Create(ctx, rr)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("more than once"))
	})

	It("rejects an unknown outputFormat via the CRD enum schema", func() {
		rr := referenceRepo("crate-badenum")
		rr.Spec.LabelRoutes = []contentremixerv1alpha1.LabelRoute{
			{Label: "recycle:newsletter", InputFormat: "auto", OutputFormat: "newsletter"},
		}
		err := k8sClient.Create(ctx, rr)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("outputFormat"))
	})

	It("rejects labelRoutes with fewer than one entry via the CRD schema", func() {
		rr := referenceRepo("crate-noroutes")
		rr.Spec.LabelRoutes = nil
		err := k8sClient.Create(ctx, rr)
		Expect(err).To(HaveOccurred())
	})

	It("rejects a malformed repo (owner/name pattern) via the CRD schema", func() {
		rr := referenceRepo("crate-badrepo")
		rr.Spec.Repo = "not-a-full-repo-path"
		err := k8sClient.Create(ctx, rr)
		Expect(err).To(HaveOccurred())
	})

	It("defaults inputFormat to auto and PRTarget to main/recycle/", func() {
		rr := referenceRepo("crate-defaults")
		rr.Spec.TokenRef = contentremixerv1alpha1.SecretKeyRef{Name: "github-pat", Key: "token"}
		rr.Spec.WebhookSecretRef = contentremixerv1alpha1.SecretKeyRef{Name: "github-webhook", Key: "hmac"}
		rr.Spec.LabelRoutes = []contentremixerv1alpha1.LabelRoute{
			{Label: "recycle:blog", OutputFormat: "blog"}, // InputFormat left empty -> default auto
		}
		Expect(k8sClient.Create(ctx, rr)).To(Succeed())

		got := &contentremixerv1alpha1.ReferenceRepo{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "crate-defaults"}, got)).To(Succeed())
			g.Expect(got.Spec.LabelRoutes[0].InputFormat).To(Equal("auto"))
			g.Expect(got.Spec.PRTarget.BaseBranch).To(Equal("main"))
			g.Expect(got.Spec.PRTarget.BranchPrefix).To(Equal("recycle/"))
			g.Expect(got.Spec.PRTarget.Draft).To(BeFalse())
		}).Should(Succeed())
	})
})
