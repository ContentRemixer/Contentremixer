// Package litellm renders the LiteLLM gateway configuration from
// ModelProfile resources. Design §7: the ModelProfile controller renders the
// `litellm-config` ConfigMap; LiteLLM mounts it and rolls on change.
//
// Subtask B ships the minimal renderer (model_list skeleton, no secrets).
// Subtask C (ISI-7078) owns the full integration: key env wiring from
// Secrets, default-selection semantics, and rollout checksums.
package litellm

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// ConfigMapName is the name of the ConfigMap the renderer produces
// (design §7: ConfigMap litellm-config, namespace-scoped to the operator).
const ConfigMapName = "litellm-config"

// ConfigMapKey is the key inside the litellm-config ConfigMap holding the
// rendered LiteLLM config.yaml.
const ConfigMapKey = "config.yaml"

// ModelEntry is one model_list entry of the LiteLLM config.
type ModelEntry struct {
	ModelName     string        `yaml:"model_name" json:"model_name"`
	LitellmParams LitellmParams `yaml:"litellm_params" json:"litellm_params"`
}

// LitellmParams carries the per-model connection parameters.
// NOTE: no secret values ever appear here (design §8) — API keys are wired
// by subtask C as env refs from the Secrets referenced by apiKeyRef.
type LitellmParams struct {
	Model   string `yaml:"model" json:"model"`
	APIBase string `yaml:"api_base,omitempty" json:"api_base,omitempty"`
}

// Config is the minimal LiteLLM config shape rendered in Phase 1 subtask B.
type Config struct {
	ModelList []ModelEntry `yaml:"model_list" json:"model_list"`
}

// RenderConfig renders the LiteLLM config body from all ModelProfiles in the
// namespace. Profiles are sorted by litellmName for a stable rendering.
func RenderConfig(profiles []contentremixerv1alpha1.ModelProfile) Config {
	sorted := make([]contentremixerv1alpha1.ModelProfile, len(profiles))
	copy(sorted, profiles)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Spec.LiteLLMName < sorted[j].Spec.LiteLLMName
	})

	cfg := Config{ModelList: make([]ModelEntry, 0, len(sorted))}
	for i := range sorted {
		p := sorted[i]
		entry := ModelEntry{
			ModelName: p.Spec.LiteLLMName,
			LitellmParams: LitellmParams{
				Model: p.Spec.Provider + "/" + p.Spec.Model,
			},
		}
		if p.Spec.APIBase != "" {
			entry.LitellmParams.APIBase = p.Spec.APIBase
		}
		cfg.ModelList = append(cfg.ModelList, entry)
	}
	return cfg
}

// RenderYAML renders the config as a YAML document string. It avoids pulling
// in a YAML dependency by emitting the small closed shape by hand; the shape
// is fixed and tested.
func RenderYAML(cfg Config) string {
	var b strings.Builder
	b.WriteString("model_list:\n")
	for _, m := range cfg.ModelList {
		b.WriteString(fmt.Sprintf("  - model_name: %s\n", m.ModelName))
		b.WriteString("    litellm_params:\n")
		b.WriteString(fmt.Sprintf("      model: %s\n", m.LitellmParams.Model))
		if m.LitellmParams.APIBase != "" {
			b.WriteString(fmt.Sprintf("      api_base: %s\n", m.LitellmParams.APIBase))
		}
	}
	if len(cfg.ModelList) == 0 {
		b.WriteString("  []\n")
	}
	return b.String()
}

// DesiredConfigMap builds the desired litellm-config ConfigMap for a
// namespace from the given profiles.
func DesiredConfigMap(namespace string, profiles []contentremixerv1alpha1.ModelProfile) *corev1.ConfigMap {
	cfg := RenderConfig(profiles)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: namespace,
		},
		Data: map[string]string{
			ConfigMapKey: RenderYAML(cfg),
		},
	}
}
