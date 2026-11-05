package litellm

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

func profile(name, provider, model, alias string, def bool, keyRef *contentremixerv1alpha1.SecretKeyRef) contentremixerv1alpha1.ModelProfile {
	return contentremixerv1alpha1.ModelProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "contentremixer"},
		Spec: contentremixerv1alpha1.ModelProfileSpec{
			Provider:    provider,
			Model:       model,
			LiteLLMName: alias,
			Default:     def,
			APIKeyRef:   keyRef,
		},
	}
}

func TestRenderConfigDefaultSelection(t *testing.T) {
	profiles := []contentremixerv1alpha1.ModelProfile{
		profile("fast", "openai", "gpt-4o-mini", "fast", false, nil),
		profile("main", "anthropic", "claude-opus-4", "default", true, nil),
	}

	cfg := RenderConfig(profiles)
	yamlOut := RenderYAML(cfg)

	if cfg.GeneralSettings == nil || cfg.GeneralSettings.DefaultModel != "default" {
		t.Fatalf("expected general_settings.default_model=default, got %+v", cfg.GeneralSettings)
	}
	if !strings.Contains(yamlOut, "default_model: default") {
		t.Fatalf("rendered YAML missing default_model:\n%s", yamlOut)
	}

	// Zero defaults: degraded, no default_model rendered (design §2.1a).
	profiles[1].Spec.Default = false
	cfg = RenderConfig(profiles)
	if cfg.GeneralSettings != nil {
		t.Fatalf("expected no general_settings with zero defaults, got %+v", cfg.GeneralSettings)
	}
	if strings.Contains(RenderYAML(cfg), "default_model") {
		t.Fatalf("rendered YAML must not contain default_model with zero defaults:\n%s", RenderYAML(cfg))
	}

	// More than one default (webhook normally prevents this): ambiguous —
	// do NOT pick a winner, no default_model rendered.
	profiles[0].Spec.Default = true
	profiles[1].Spec.Default = true
	cfg = RenderConfig(profiles)
	if cfg.GeneralSettings != nil {
		t.Fatalf("expected no general_settings with duplicate defaults, got %+v", cfg.GeneralSettings)
	}
}

func TestRenderConfigStableOrdering(t *testing.T) {
	profiles := []contentremixerv1alpha1.ModelProfile{
		profile("zeta", "openai", "gpt-4o", "zeta", false, nil),
		profile("alpha", "openai", "gpt-4o", "alpha", false, nil),
	}
	first := RenderYAML(RenderConfig(profiles))
	second := RenderYAML(RenderConfig([]contentremixerv1alpha1.ModelProfile{profiles[1], profiles[0]}))
	if first != second {
		t.Fatalf("rendering must be order-stable:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestAPIKeyEnvIndirection(t *testing.T) {
	ref := &contentremixerv1alpha1.SecretKeyRef{Name: "openai-apikey", Key: "key"}
	profiles := []contentremixerv1alpha1.ModelProfile{
		profile("fast", "openai", "gpt-4o-mini", "fast", false, ref),
		profile("long", "ollama", "llama3.1:70b", "long-context", false, nil),
	}

	cfg := RenderConfig(profiles)
	yamlOut := RenderYAML(cfg)

	if !strings.Contains(yamlOut, "api_key: os.environ/MODEL_KEY_FAST") {
		t.Fatalf("expected api_key env indirection for keyed profile:\n%s", yamlOut)
	}
	if strings.Contains(yamlOut, "MODEL_KEY_LONG_CONTEXT") {
		t.Fatalf("keyless profile must not get an api_key:\n%s", yamlOut)
	}
	if strings.Contains(yamlOut, "openai-apikey") {
		t.Fatalf("Secret name must never leak into the rendered config:\n%s", yamlOut)
	}

	envs := DesiredEnv(profiles)
	if len(envs) != 1 {
		t.Fatalf("expected exactly 1 env var, got %d", len(envs))
	}
	e := envs[0]
	if e.Name != "MODEL_KEY_FAST" || e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("unexpected env var %+v", e)
	}
	sr := e.ValueFrom.SecretKeyRef
	if sr.Name != "openai-apikey" || sr.Key != "key" {
		t.Fatalf("env must reference the SecretKeyRef verbatim, got %s/%s", sr.Name, sr.Key)
	}
}

func TestAPIKeyEnvNameInjective(t *testing.T) {
	got := APIKeyEnvName("long-context")
	if got != "MODEL_KEY_LONG_CONTEXT" {
		t.Fatalf("APIKeyEnvName(long-context) = %s", got)
	}
	// CRD pattern forbids underscores, so '-'→'_' is collision-free.
	if APIKeyEnvName("fast") == APIKeyEnvName("fast-model") {
		t.Fatal("env names must be injective over the litellmName pattern")
	}
}

func TestChecksumRollsOnConfigChange(t *testing.T) {
	a := RenderYAML(RenderConfig([]contentremixerv1alpha1.ModelProfile{
		profile("main", "anthropic", "claude-opus-4", "default", true, nil),
	}))
	// Same config, same checksum.
	if Checksum(a) != Checksum(RenderYAML(RenderConfig([]contentremixerv1alpha1.ModelProfile{
		profile("main", "anthropic", "claude-opus-4", "default", true, nil),
	}))) {
		t.Fatal("identical configs must checksum identically")
	}
	b := RenderYAML(RenderConfig([]contentremixerv1alpha1.ModelProfile{
		profile("main", "anthropic", "claude-sonnet-4", "default", true, nil),
	}))
	if Checksum(a) == Checksum(b) {
		t.Fatal("config change must change the rollout checksum")
	}
	if len(Checksum(a)) != 64 {
		t.Fatalf("checksum must be sha256 hex, got %q", Checksum(a))
	}
}

func TestDesiredDeploymentWiring(t *testing.T) {
	ref := &contentremixerv1alpha1.SecretKeyRef{Name: "openai-apikey", Key: "key"}
	profiles := []contentremixerv1alpha1.ModelProfile{
		profile("fast", "openai", "gpt-4o-mini", "fast", false, ref),
		profile("main", "anthropic", "claude-opus-4", "default", true, nil),
	}
	rendered := RenderYAML(RenderConfig(profiles))

	dep := DesiredDeployment("contentremixer", profiles, rendered)
	if dep.Name != DeploymentName || dep.Namespace != "contentremixer" {
		t.Fatalf("unexpected deployment meta %s/%s", dep.Namespace, dep.Name)
	}
	if got := dep.Spec.Template.Annotations[ConfigChecksumAnnotation]; got != Checksum(rendered) {
		t.Fatalf("checksum annotation %q != %q", got, Checksum(rendered))
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if c.Image != Image {
		t.Fatalf("image not pinned: %s", c.Image)
	}
	if len(c.Env) != 1 || c.Env[0].Name != "MODEL_KEY_FAST" {
		t.Fatalf("expected MODEL_KEY_FAST env on container, got %+v", c.Env)
	}
	if len(dep.Spec.Template.Spec.Volumes) != 1 || dep.Spec.Template.Spec.Volumes[0].ConfigMap.Name != ConfigMapName {
		t.Fatalf("config volume must mount %s", ConfigMapName)
	}
}
