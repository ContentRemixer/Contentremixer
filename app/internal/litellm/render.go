// Package litellm renders the LiteLLM gateway configuration from
// ModelProfile resources. Design §7: the ModelProfile controller renders the
// `litellm-config` ConfigMap; LiteLLM mounts it and rolls on change.
//
// Subtask C (ISI-7078) full integration:
//   - per-model API keys wired as env refs from the Secrets referenced by
//     ModelProfile.spec.apiKeyRef (design §8: zero secret values rendered),
//   - default-model selection via general_settings.default_model, honored
//     from the ModelProfile with spec.default=true,
//   - rollout: the LiteLLM Deployment carries a config checksum annotation
//     so pods roll whenever the rendered config changes.
package litellm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// ConfigMapName is the name of the ConfigMap the renderer produces
// (design §7: ConfigMap litellm-config, namespace-scoped to the operator).
const ConfigMapName = "litellm-config"

// ConfigMapKey is the key inside the litellm-config ConfigMap holding the
// rendered LiteLLM config.yaml.
const ConfigMapKey = "config.yaml"

// DeploymentName is the name of the LiteLLM gateway Deployment the
// controller owns (env wiring + rollout checksum annotation, design §7).
const DeploymentName = "litellm"

// ContainerName is the LiteLLM container inside the gateway Deployment.
const ContainerName = "litellm"

// ConfigChecksumAnnotation is set on the gateway pod template to the sha256
// of the rendered config; a change rolls the Deployment.
const ConfigChecksumAnnotation = "contentremixer.io/litellm-config-checksum"

// Image is the pinned LiteLLM gateway image (pinned in subtask C per the
// deploy/litellm/litellm.yaml TODO; bump deliberately).
const Image = "ghcr.io/berriai/litellm:main-v1.82.3"

// ServingPort is the port the LiteLLM gateway serves on (design §7).
const ServingPort = 4000

// ModelEntry is one model_list entry of the LiteLLM config.
type ModelEntry struct {
	ModelName     string        `yaml:"model_name" json:"model_name"`
	LitellmParams LitellmParams `yaml:"litellm_params" json:"litellm_params"`
}

// LitellmParams carries the per-model connection parameters.
// NOTE: no secret values ever appear here (design §8) — api_key is rendered
// as the LiteLLM env indirection `os.environ/<VAR>` and the corresponding
// env var is wired on the Deployment from the Secret referenced by
// ModelProfile.spec.apiKeyRef.
type LitellmParams struct {
	Model   string `yaml:"model" json:"model"`
	APIBase string `yaml:"api_base,omitempty" json:"api_base,omitempty"`
	APIKey  string `yaml:"api_key,omitempty" json:"api_key,omitempty"`
}

// GeneralSettings is the LiteLLM general_settings block. DefaultModel is the
// alias LiteLLM serves when a request carries no model — rendered from the
// ModelProfile with spec.default=true (exactly-one-default invariant,
// design §2.1a).
type GeneralSettings struct {
	DefaultModel string `yaml:"default_model" json:"default_model"`
}

// Config is the LiteLLM config shape rendered from all ModelProfiles.
type Config struct {
	ModelList       []ModelEntry     `yaml:"model_list" json:"model_list"`
	GeneralSettings *GeneralSettings `yaml:"general_settings,omitempty" json:"general_settings,omitempty"`
}

// APIKeyEnvName returns the deterministic env var name that carries the API
// key for a litellmName alias, e.g. "fast" -> MODEL_KEY_FAST,
// "long-context" -> MODEL_KEY_LONG_CONTEXT. The CRD pattern forbids
// underscores, so the mapping is injective.
func APIKeyEnvName(litellmName string) string {
	r := strings.NewReplacer("-", "_")
	return "MODEL_KEY_" + strings.ToUpper(r.Replace(litellmName))
}

// RenderConfig renders the LiteLLM config body from all ModelProfiles in the
// namespace. Profiles are sorted by litellmName for a stable rendering.
// When exactly one profile sets spec.default=true its alias becomes
// general_settings.default_model; zero defaults renders no default_model
// (degraded, controller surfaces DefaultMissing — design §2.1a); more than
// one default never passes the webhook and is left unrendered here too.
func RenderConfig(profiles []contentremixerv1alpha1.ModelProfile) Config {
	sorted := make([]contentremixerv1alpha1.ModelProfile, len(profiles))
	copy(sorted, profiles)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Spec.LiteLLMName < sorted[j].Spec.LiteLLMName
	})

	defaults := 0
	defaultAlias := ""
	cfg := Config{ModelList: make([]ModelEntry, 0, len(sorted))}
	for i := range sorted {
		p := sorted[i]
		if p.Spec.Default {
			defaults++
			defaultAlias = p.Spec.LiteLLMName
		}
		entry := ModelEntry{
			ModelName: p.Spec.LiteLLMName,
			LitellmParams: LitellmParams{
				Model: p.Spec.Provider + "/" + p.Spec.Model,
			},
		}
		if p.Spec.APIBase != "" {
			entry.LitellmParams.APIBase = p.Spec.APIBase
		}
		if p.Spec.APIKeyRef != nil {
			// Env indirection only — the key VALUE lives in a Secret wired
			// as Deployment env (design §8). Never rendered into the config.
			entry.LitellmParams.APIKey = "os.environ/" + APIKeyEnvName(p.Spec.LiteLLMName)
		}
		cfg.ModelList = append(cfg.ModelList, entry)
	}
	if defaults == 1 {
		cfg.GeneralSettings = &GeneralSettings{DefaultModel: defaultAlias}
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
		if m.LitellmParams.APIKey != "" {
			b.WriteString(fmt.Sprintf("      api_key: %s\n", m.LitellmParams.APIKey))
		}
	}
	if len(cfg.ModelList) == 0 {
		b.WriteString("  []\n")
	}
	if cfg.GeneralSettings != nil {
		b.WriteString("general_settings:\n")
		b.WriteString(fmt.Sprintf("  default_model: %s\n", cfg.GeneralSettings.DefaultModel))
	}
	return b.String()
}

// Checksum returns the sha256 hex digest of a rendered config body, used as
// the rollout checksum annotation value on the gateway Deployment.
func Checksum(rendered string) string {
	sum := sha256.Sum256([]byte(rendered))
	return hex.EncodeToString(sum[:])
}

// DesiredEnv returns the env vars to set on the gateway container: one
// MODEL_KEY_<ALIAS> per profile that references an API-key Secret. Env vars
// are sorted by name for a stable, diffable shape. A referenced-but-missing
// Secret intentionally leaves the pod in CreateContainerConfigError (loud,
// fail-closed) rather than starting a gateway with a dead model.
func DesiredEnv(profiles []contentremixerv1alpha1.ModelProfile) []corev1.EnvVar {
	envs := make([]corev1.EnvVar, 0, len(profiles))
	for i := range profiles {
		p := profiles[i]
		if p.Spec.APIKeyRef == nil {
			continue
		}
		envs = append(envs, corev1.EnvVar{
			Name: APIKeyEnvName(p.Spec.LiteLLMName),
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: p.Spec.APIKeyRef.Name,
					},
					Key: p.Spec.APIKeyRef.Key,
				},
			},
		})
	}
	sort.Slice(envs, func(i, j int) bool { return envs[i].Name < envs[j].Name })
	return envs
}

// DesiredConfigMap builds the desired litellm-config ConfigMap for a
// namespace from the given profiles.
func DesiredConfigMap(namespace string, profiles []contentremixerv1alpha1.ModelProfile) *corev1.ConfigMap {
	cfg := RenderConfig(profiles)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":    "litellm",
				"app.kubernetes.io/part-of": "contentremixer",
			},
		},
		Data: map[string]string{
			ConfigMapKey: RenderYAML(cfg),
		},
	}
}

// DesiredDeployment builds the desired LiteLLM gateway Deployment: config
// mounted from the litellm-config ConfigMap, per-model API keys as env from
// Secrets, and the rendered-config checksum annotation so any config change
// rolls the pods (design §7 config flow).
func DesiredDeployment(namespace string, profiles []contentremixerv1alpha1.ModelProfile, renderedConfig string) *appsv1.Deployment {
	checksum := Checksum(renderedConfig)
	ports := intstr.FromInt(ServingPort)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DeploymentName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":    "litellm",
				"app.kubernetes.io/part-of": "contentremixer",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app.kubernetes.io/name": "litellm"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name":    "litellm",
						"app.kubernetes.io/part-of": "contentremixer",
					},
					Annotations: map[string]string{
						ConfigChecksumAnnotation: checksum,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  ContainerName,
							Image: Image,
							Args:  []string{"--config", "/etc/litellm/config.yaml", "--port", strconv.Itoa(ServingPort)},
							Env:   DesiredEnv(profiles),
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: ServingPort},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "config", MountPath: "/etc/litellm", ReadOnly: true},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{Path: "/health/liveliness", Port: ports},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       10,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resourceQty("100m"),
									corev1.ResourceMemory: resourceQty("128Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resourceQty("1"),
									corev1.ResourceMemory: resourceQty("512Mi"),
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: ConfigMapName},
								},
							},
						},
					},
				},
			},
		},
	}
}

func ptr[T any](v T) *T { return &v }

func resourceQty(s string) resource.Quantity {
	return resource.MustParse(s)
}
