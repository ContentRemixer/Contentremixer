package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
)

func modelProfile(name string, def bool) *contentremixerv1alpha1.ModelProfile {
	return &contentremixerv1alpha1.ModelProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: contentremixerv1alpha1.ModelProfileSpec{
			Provider:    "anthropic",
			Model:       "claude-opus-4",
			LiteLLMName: name,
			Default:     def,
		},
	}
}

var _ = Describe("ModelProfile controller + webhook (envtest)", func() {
	It("renders all profiles into the litellm-config ConfigMap and sets Ready", func() {
		mp := modelProfile("default", true)
		Expect(k8sClient.Create(ctx, mp)).To(Succeed())

		got := &contentremixerv1alpha1.ModelProfile{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "default"}, got)).To(Succeed())
			g.Expect(got.Status.Rendered).To(BeTrue())
			ready := meta.FindStatusCondition(got.Status.Conditions, "Ready")
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())

		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: litellm.ConfigMapName}, cm)).To(Succeed())
		Expect(cm.Data).To(HaveKey(litellm.ConfigMapKey))
		Expect(cm.Data[litellm.ConfigMapKey]).To(ContainSubstring("model_name: default"))
		Expect(cm.Data[litellm.ConfigMapKey]).To(ContainSubstring("model: anthropic/claude-opus-4"))
		// Default selection (subtask C): spec.default=true renders as
		// general_settings.default_model.
		Expect(cm.Data[litellm.ConfigMapKey]).To(ContainSubstring("general_settings:"))
		Expect(cm.Data[litellm.ConfigMapKey]).To(ContainSubstring("default_model: default"))
	})

	It("rejects a second default via the validating webhook", func() {
		mp := modelProfile("second-default", true)
		err := k8sClient.Create(ctx, mp)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("already the default"))
	})

	It("rejects a duplicate litellmName via the validating webhook", func() {
		mp := modelProfile("alias-thief", false)
		mp.Spec.LiteLLMName = "default" // already claimed by the "default" profile
		err := k8sClient.Create(ctx, mp)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("litellmName"))
	})

	It("accepts a non-default profile and re-renders the ConfigMap", func() {
		mp := modelProfile("fast", false)
		mp.Spec.Provider = "openai"
		mp.Spec.Model = "gpt-4o-mini"
		Expect(k8sClient.Create(ctx, mp)).To(Succeed())

		cm := &corev1.ConfigMap{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: litellm.ConfigMapName}, cm)).To(Succeed())
			g.Expect(cm.Data[litellm.ConfigMapKey]).To(ContainSubstring("model_name: fast"))
			g.Expect(cm.Data[litellm.ConfigMapKey]).To(ContainSubstring("model: openai/gpt-4o-mini"))
		}).Should(Succeed())
	})

	It("surfaces DefaultMissing when no profile is default (degraded, still rendered)", func() {
		// Unset the existing default.
		mp := &contentremixerv1alpha1.ModelProfile{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "default"}, mp)).To(Succeed())
		mp.Spec.Default = false
		Expect(k8sClient.Update(ctx, mp)).To(Succeed())

		got := &contentremixerv1alpha1.ModelProfile{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "fast"}, got)).To(Succeed())
			missing := meta.FindStatusCondition(got.Status.Conditions, "DefaultMissing")
			g.Expect(missing).NotTo(BeNil())
			g.Expect(missing.Status).To(Equal(metav1.ConditionTrue))
			// Still rendered — degradation does not stop rendering.
			g.Expect(got.Status.Rendered).To(BeTrue())
		}).Should(Succeed())

		// Restore the default for later specs.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "default"}, mp)).To(Succeed())
		mp.Spec.Default = true
		Expect(k8sClient.Update(ctx, mp)).To(Succeed())
	})

	It("rejects an invalid LiteLLMName pattern via the CRD schema", func() {
		mp := modelProfile("Bad_Name", false)
		err := k8sClient.Create(ctx, mp)
		Expect(err).To(HaveOccurred())
	})

	It("keeps spec.owner (platform/Admin when empty)", func() {
		mp := modelProfile("owned", false)
		subject := "oidc-sub-42"
		mp.Spec.Owner = contentremixerv1alpha1.OwnerRef{Subject: subject}
		Expect(k8sClient.Create(ctx, mp)).To(Succeed())

		// k8sClient reads through the manager cache; tolerate read-after-write
		// lag instead of racing the informer (suite_test.go note).
		got := &contentremixerv1alpha1.ModelProfile{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "owned"}, got)).To(Succeed())
			g.Expect(got.Spec.Owner.Subject).To(Equal(subject))
			g.Expect(client.ObjectKeyFromObject(got)).To(Equal(types.NamespacedName{Namespace: ns, Name: "owned"}))
		}).Should(Succeed())
	})

	It("defaults and validates owner is optional (empty means platform)", func() {
		mp := modelProfile("platform-owned", false)
		Expect(k8sClient.Create(ctx, mp)).To(Succeed())
		got := &contentremixerv1alpha1.ModelProfile{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "platform-owned"}, got)).To(Succeed())
			g.Expect(got.Spec.Owner.Subject).To(BeEmpty())
		}).Should(Succeed())
	})

	It("deploys the gateway: env from Secret + config checksum annotation (subtask C)", func() {
		// A keyed profile whose Secret does not exist yet.
		mp := modelProfile("keyed", false)
		mp.Spec.Provider = "openai"
		mp.Spec.Model = "gpt-4o"
		mp.Spec.APIKeyRef = &contentremixerv1alpha1.SecretKeyRef{Name: "model-apikey", Key: "key"}
		Expect(k8sClient.Create(ctx, mp)).To(Succeed())

		// Fail-closed: missing Secret surfaces APIKeySecretMissing on the owner.
		got := &contentremixerv1alpha1.ModelProfile{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "keyed"}, got)).To(Succeed())
			ready := meta.FindStatusCondition(got.Status.Conditions, "Ready")
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(ready.Reason).To(Equal("APIKeySecretMissing"))
		}).Should(Succeed())

		// The gateway Deployment exists regardless (design §7: deploy), with
		// the env indirection wired and the rollout checksum annotation set.
		dep := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: litellm.DeploymentName}, dep)).To(Succeed())
		Expect(dep.Spec.Template.Annotations).To(HaveKey(litellm.ConfigChecksumAnnotation))
		Expect(dep.Spec.Template.Annotations[litellm.ConfigChecksumAnnotation]).NotTo(BeEmpty())

		// Rendered config carries the env indirection, never the Secret name.
		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: litellm.ConfigMapName}, cm)).To(Succeed())
		Expect(cm.Data[litellm.ConfigMapKey]).To(ContainSubstring("api_key: os.environ/MODEL_KEY_KEYED"))
		Expect(cm.Data[litellm.ConfigMapKey]).NotTo(ContainSubstring("model-apikey"))

		// Env is wired on the container from the referenced Secret.
		Eventually(func(g Gomega) {
			d := &appsv1.Deployment{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: litellm.DeploymentName}, d)).To(Succeed())
			c := containerByName(d, litellm.ContainerName)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Env).NotTo(BeEmpty())
			found := false
			for _, e := range c.Env {
				if e.Name == "MODEL_KEY_KEYED" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil &&
					e.ValueFrom.SecretKeyRef.Name == "model-apikey" && e.ValueFrom.SecretKeyRef.Key == "key" {
					found = true
				}
			}
			g.Expect(found).To(BeTrue(), "MODEL_KEY_KEYED env from Secret must be wired: %+v", c.Env)
		}).Should(Succeed())

		// Rollout: checksum annotation changes when the rendered config changes.
		before := dep.Spec.Template.Annotations[litellm.ConfigChecksumAnnotation]
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "keyed"}, got)).To(Succeed())
		got.Spec.Model = "gpt-4.1"
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
		Eventually(func(g Gomega) {
			d := &appsv1.Deployment{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: litellm.DeploymentName}, d)).To(Succeed())
			g.Expect(d.Spec.Template.Annotations[litellm.ConfigChecksumAnnotation]).NotTo(Equal(before))
			g.Expect(d.Spec.Template.Annotations[litellm.ConfigChecksumAnnotation]).NotTo(BeEmpty())
		}).Should(Succeed())

		// Recovery: creating the referenced Secret flips Ready back to true
		// (Secret watch re-reconciles).
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "model-apikey", Namespace: ns},
			StringData: map[string]string{"key": "placeholder-not-a-real-key"},
		}
		Expect(k8sClient.Create(ctx, sec)).To(Succeed())
		Eventually(func(g Gomega) {
			p := &contentremixerv1alpha1.ModelProfile{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "keyed"}, p)).To(Succeed())
			ready := meta.FindStatusCondition(p.Status.Conditions, "Ready")
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
	})
})

func containerByName(d *appsv1.Deployment, name string) *corev1.Container {
	for i := range d.Spec.Template.Spec.Containers {
		if d.Spec.Template.Spec.Containers[i].Name == name {
			return &d.Spec.Template.Spec.Containers[i]
		}
	}
	return nil
}
