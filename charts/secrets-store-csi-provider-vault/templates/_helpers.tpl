{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{- define "podplane.vaultProviderCACertConfigMapName" -}}
{{- printf "podplane-secrets-provider-ca-%s" . -}}
{{- end -}}
