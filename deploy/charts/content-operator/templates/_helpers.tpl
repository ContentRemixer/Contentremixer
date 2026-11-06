{{/*
Install namespace.
*/}}
{{- define "content-operator.namespace" -}}
{{- .Values.namespace.name -}}
{{- end -}}

{{/*
Chart label value (name-version, sanitised per Helm convention).
*/}}
{{- define "content-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels applied to every object. Merges in .Values.commonLabels.
*/}}
{{- define "content-operator.labels" -}}
helm.sh/chart: {{ include "content-operator.chart" . }}
app.kubernetes.io/part-of: contentremixer
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{/*
Operator component labels (adds the name label on top of the common set).
*/}}
{{- define "content-operator.operatorLabels" -}}
app.kubernetes.io/name: {{ .Values.operator.name }}
{{ include "content-operator.labels" . }}
{{- end -}}

{{/*
Operator selector labels — the stable subset used by Deployment/Service
selectors. MUST stay a subset of the pod template labels.
*/}}
{{- define "content-operator.operatorSelectorLabels" -}}
app.kubernetes.io/name: {{ .Values.operator.name }}
{{- end -}}

{{/*
Fully-qualified operator image (tag falls back to .Chart.AppVersion).
*/}}
{{- define "content-operator.operatorImage" -}}
{{- $tag := .Values.operator.image.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s" .Values.operator.image.repository $tag -}}
{{- end -}}

{{/*
ServiceAccount name.
*/}}
{{- define "content-operator.serviceAccountName" -}}
{{- .Values.serviceAccount.name | default .Values.operator.name -}}
{{- end -}}

{{/*
LiteLLM image.
*/}}
{{- define "content-operator.litellmImage" -}}
{{- printf "%s:%s" .Values.litellm.image.repository .Values.litellm.image.tag -}}
{{- end -}}

{{/*
Derive the ReferenceRepo CR name (a DNS-1123 label) for a referenceRepos entry.
Input: a dict {"repo": "owner/name", "name": "<optional override>"}.
Default = the repo's name part (after the "/"), lower-cased, with any char
outside [a-z0-9-] collapsed to "-" and leading/trailing "-" trimmed. The same
derivation is what hack/onboard-content-repo.sh and the per-repo Secret names
(<name>-github-pat / <name>-webhook-hmac) are keyed on — keep them in sync.
*/}}
{{- define "content-operator.referenceRepoName" -}}
{{- if .name -}}
{{- .name -}}
{{- else -}}
{{- $leaf := last (splitList "/" .repo) -}}
{{- regexReplaceAll "[^a-z0-9-]" (lower $leaf) "-" | trunc 63 | trimPrefix "-" | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
