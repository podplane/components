{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0
*/}}
{{- define "secrets-store-csi-provider-gcp.labels" -}}
app.kubernetes.io/name: secrets-store-csi-provider-gcp
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "secrets-store-csi-provider-gcp.selectorLabels" -}}
app.kubernetes.io/name: secrets-store-csi-provider-gcp
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
