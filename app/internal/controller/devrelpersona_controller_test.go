package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

func devRelPersona(name, subject string, refs ...contentremixerv1alpha1.PersonaReference) *contentremixerv1alpha1.DevRelPersona {
	return &contentremixerv1alpha1.DevRelPersona{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: contentremixerv1alpha1.DevRelPersonaSpec{
			Owner:           contentremixerv1alpha1.OwnerRef{Subject: subject},
			SelfDescription: "pragmatic, example-first, dry humour",
			References:      refs,
		},
	}
}

func ref(url, kind string) contentremixerv1alpha1.PersonaReference {
	return contentremixerv1alpha1.PersonaReference{URL: url, Kind: kind}
}

var _ = Describe("DevRelPersona controller + webhook (envtest)", func() {
	It("accepts an owned persona and enqueues ingestion (Pending + IngestionPending) on first apply", func() {
		p := devRelPersona("alice-persona", "oidc|alice",
			ref("https://youtu.be/talk1", "youtube"),
			ref("https://example.com/post", "article"))
		Expect(k8sClient.Create(ctx, p)).To(Succeed())

		got := &contentremixerv1alpha1.DevRelPersona{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "alice-persona"}, got)).To(Succeed())
			g.Expect(got.Status.Phase).To(Equal(contentremixerv1alpha1.PersonaPhasePending))

			accepted := meta.FindStatusCondition(got.Status.Conditions, contentremixerv1alpha1.PersonaConditionAccepted)
			g.Expect(accepted).NotTo(BeNil())
			g.Expect(accepted.Status).To(Equal(metav1.ConditionTrue))

			pending := meta.FindStatusCondition(got.Status.Conditions, contentremixerv1alpha1.PersonaConditionIngestionPending)
			g.Expect(pending).NotTo(BeNil())
			g.Expect(pending.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(pending.Reason).To(Equal("ReferencesChanged"))

			ready := meta.FindStatusCondition(got.Status.Conditions, contentremixerv1alpha1.PersonaConditionReady)
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))

			g.Expect(got.Status.ObservedReferenceHash).NotTo(BeEmpty())
		}).Should(Succeed())

		// P2-A owns neither exemplarCount nor lastEmbeddedAt.
		Expect(got.Status.ExemplarCount).To(Equal(0))
		Expect(got.Status.LastEmbeddedAt).To(BeNil())
		// spec round-trips.
		Expect(got.Spec.Owner.Subject).To(Equal("oidc|alice"))
		Expect(got.Spec.SelfDescription).To(ContainSubstring("example-first"))
		Expect(got.Spec.References).To(HaveLen(2))
	})

	It("does not re-trigger once a sibling advances the phase (steady-state resync is inert)", func() {
		p := devRelPersona("bob-persona", "oidc|bob", ref("https://youtu.be/talk", "youtube"))
		Expect(k8sClient.Create(ctx, p)).To(Succeed())

		got := &contentremixerv1alpha1.DevRelPersona{}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "bob-persona"}, got)).To(Succeed())
			g.Expect(got.Status.Phase).To(Equal(contentremixerv1alpha1.PersonaPhasePending))
			g.Expect(got.Status.ObservedReferenceHash).NotTo(BeEmpty())
		}).Should(Succeed())

		// Simulate P2-C completing the hand-off: advance phase to Ready and
		// clear IngestionPending. P2-A must NOT revert this on resync because
		// the reference hash is unchanged. Retry the read-modify-write to ride
		// out reconciler status writes racing the shared cache.
		Eventually(func(g Gomega) {
			cur := &contentremixerv1alpha1.DevRelPersona{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "bob-persona"}, cur)).To(Succeed())
			cur.Status.Phase = contentremixerv1alpha1.PersonaPhaseReady
			cur.Status.ExemplarCount = 3
			meta.SetStatusCondition(&cur.Status.Conditions, metav1.Condition{
				Type: contentremixerv1alpha1.PersonaConditionIngestionPending, Status: metav1.ConditionFalse,
				Reason: "Embedded", Message: "done",
			})
			meta.SetStatusCondition(&cur.Status.Conditions, metav1.Condition{
				Type: contentremixerv1alpha1.PersonaConditionReady, Status: metav1.ConditionTrue,
				Reason: "Embedded", Message: "index ready",
			})
			g.Expect(k8sClient.Status().Update(ctx, cur)).To(Succeed())
		}).Should(Succeed())

		// Wait for the sibling write to land in the shared cache (read-after-write
		// lag), then assert P2-A does NOT revert it over a reconcile window.
		Eventually(func(g Gomega) {
			cur := &contentremixerv1alpha1.DevRelPersona{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "bob-persona"}, cur)).To(Succeed())
			g.Expect(cur.Status.Phase).To(Equal(contentremixerv1alpha1.PersonaPhaseReady))
			g.Expect(cur.Status.ExemplarCount).To(Equal(3))
		}).Should(Succeed())

		Consistently(func(g Gomega) {
			cur := &contentremixerv1alpha1.DevRelPersona{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "bob-persona"}, cur)).To(Succeed())
			g.Expect(cur.Status.Phase).To(Equal(contentremixerv1alpha1.PersonaPhaseReady))
			g.Expect(cur.Status.ExemplarCount).To(Equal(3))
			pending := meta.FindStatusCondition(cur.Status.Conditions, contentremixerv1alpha1.PersonaConditionIngestionPending)
			g.Expect(pending).NotTo(BeNil())
			g.Expect(pending.Status).To(Equal(metav1.ConditionFalse))
		}, "1s", "100ms").Should(Succeed())
	})

	It("re-enqueues (Pending again) when the reference list changes", func() {
		p := devRelPersona("carol-persona", "oidc|carol", ref("https://youtu.be/a", "youtube"))
		Expect(k8sClient.Create(ctx, p)).To(Succeed())

		got := &contentremixerv1alpha1.DevRelPersona{}
		var firstHash string
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "carol-persona"}, got)).To(Succeed())
			g.Expect(got.Status.ObservedReferenceHash).NotTo(BeEmpty())
			firstHash = got.Status.ObservedReferenceHash
		}).Should(Succeed())

		// Mark it "done" so Pending would be a real re-trigger (retry past the
		// reconciler's own status writes).
		Eventually(func(g Gomega) {
			cur := &contentremixerv1alpha1.DevRelPersona{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "carol-persona"}, cur)).To(Succeed())
			cur.Status.Phase = contentremixerv1alpha1.PersonaPhaseReady
			meta.SetStatusCondition(&cur.Status.Conditions, metav1.Condition{
				Type: contentremixerv1alpha1.PersonaConditionIngestionPending, Status: metav1.ConditionFalse, Reason: "Embedded",
			})
			g.Expect(k8sClient.Status().Update(ctx, cur)).To(Succeed())
		}).Should(Succeed())

		// Add a reference (retry on conflict against concurrent reconciles).
		Eventually(func(g Gomega) {
			cur := &contentremixerv1alpha1.DevRelPersona{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "carol-persona"}, cur)).To(Succeed())
			cur.Spec.References = append(cur.Spec.References, ref("https://example.com/new", "article"))
			g.Expect(k8sClient.Update(ctx, cur)).To(Succeed())
		}).Should(Succeed())

		Eventually(func(g Gomega) {
			cur := &contentremixerv1alpha1.DevRelPersona{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "carol-persona"}, cur)).To(Succeed())
			g.Expect(cur.Status.Phase).To(Equal(contentremixerv1alpha1.PersonaPhasePending))
			g.Expect(cur.Status.ObservedReferenceHash).NotTo(Equal(firstHash))
			pending := meta.FindStatusCondition(cur.Status.Conditions, contentremixerv1alpha1.PersonaConditionIngestionPending)
			g.Expect(pending.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
	})

	It("goes Idle (no ingestion) for an owned persona with no references", func() {
		p := devRelPersona("dave-persona", "oidc|dave")
		Expect(k8sClient.Create(ctx, p)).To(Succeed())

		Eventually(func(g Gomega) {
			cur := &contentremixerv1alpha1.DevRelPersona{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "dave-persona"}, cur)).To(Succeed())
			g.Expect(cur.Status.Phase).To(Equal(contentremixerv1alpha1.PersonaPhaseIdle))
			pending := meta.FindStatusCondition(cur.Status.Conditions, contentremixerv1alpha1.PersonaConditionIngestionPending)
			g.Expect(pending).NotTo(BeNil())
			g.Expect(pending.Status).To(Equal(metav1.ConditionFalse))
		}).Should(Succeed())
	})

	It("rejects a persona without an owner via the validating webhook", func() {
		p := devRelPersona("no-owner", "")
		err := k8sClient.Create(ctx, p)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("owner"))
	})

	It("rejects an unknown reference kind via the CRD enum schema", func() {
		p := devRelPersona("bad-kind", "oidc|erin", ref("https://x", "tweet"))
		err := k8sClient.Create(ctx, p)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("kind"))
	})
})
