{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0
*/}}
{{- define "podplane-operator.name" -}}
podplane-operator
{{- end -}}

{{- define "podplane-operator.namePrefix" -}}
{{- .Values.podplane.operator.namePrefix | default "platform-podplane-operator" -}}
{{- end -}}

{{- define "podplane-operator.labels" -}}
app.kubernetes.io/name: {{ include "podplane-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: operator
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "podplane-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "podplane-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: operator
{{- end -}}

{{- define "podplane-operator.rbac.defaultRoles.namePrefix" -}}
{{- .Values.podplane.operator.rbac.defaultRoles.namePrefix | default "platform-podplane-secrets" -}}
{{- end -}}

{{- define "podplane-operator.serviceAccountName" -}}
{{- .Values.podplane.operator.serviceAccountName | default "platform-podplane-operator" -}}
{{- end -}}

{{- define "podplane-operator.spcRestrictionPolicyName" -}}
{{ include "podplane-operator.namePrefix" . }}-spc-restriction-vap
{{- end -}}

{{- define "podplane-operator.spcRestrictionBindingName" -}}
{{ include "podplane-operator.namePrefix" . }}-spc-restriction-vapb
{{- end -}}

{{- define "podplane-operator.configJSON" -}}
{{- $valuesConfig := .Values.podplane.operator.config | default dict -}}
{{- $config := dict
  "cluster_id" ""
  "key_rotation" "6h"
  "allow_sync_to_kubernetes_secrets" false
  "providers" dict
-}}
{{- range $key, $value := $valuesConfig -}}
{{- if ne $key "providers" -}}
{{- $_ := set $config (snakecase $key) $value -}}
{{- end -}}
{{- end -}}
{{- $providers := dict -}}
{{- range $name, $provider := ($valuesConfig.providers | default dict) -}}
{{- $entry := dict -}}
{{- range $key, $value := $provider -}}
{{- $_ := set $entry (snakecase $key) $value -}}
{{- end -}}
{{- $_ := set $providers $name $entry -}}
{{- end -}}
{{- $_ := set $config "providers" $providers -}}
{{- toPrettyJson $config -}}
{{- end -}}
