{{/*
Podplane <https://podplane.dev>
Copyright 2026 Nadrama Pty Ltd
SPDX-License-Identifier: Apache-2.0
*/}}

{{/* Returns the required DNS zone for a configured ingress domain. */}}
{{- define "podplane.traefik.domainName" -}}
{{- required "platform.traefik.ingress.domains[].zone is required" .zone -}}
{{- end -}}

{{/* Converts a domain zone into a stable Kubernetes-name-safe suffix. */}}
{{- define "podplane.traefik.domainSlug" -}}
{{- $zone := include "podplane.traefik.domainName" . -}}
{{- $slug := trimAll "-" (regexReplaceAll "[^a-z0-9-]+" ($zone | lower) "-") -}}
{{- if not $slug }}{{ fail "platform.traefik.ingress.domains[].zone must contain at least one DNS-safe character" }}{{- end -}}
{{- if gt (len $slug) 42 -}}
{{- printf "%s-%s" (trunc 33 $slug | trimSuffix "-") (sha256sum $slug | trunc 8) -}}
{{- else -}}
{{- $slug -}}
{{- end -}}
{{- end -}}

{{/* Returns the cert-manager Certificate resource name for a domain. */}}
{{- define "podplane.traefik.certificateName" -}}
{{- printf "platform-traefik-%s" (include "podplane.traefik.domainSlug" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Returns the TLS Secret name shared by Gateway, bootstrap TLS, and cert-manager. */}}
{{- define "podplane.traefik.secretName" -}}
{{- printf "%s-tls" (include "podplane.traefik.certificateName" .) -}}
{{- end -}}

{{/* Returns the suffix used for Gateway listener names. */}}
{{- define "podplane.traefik.listenerSuffix" -}}
{{- include "podplane.traefik.domainSlug" . -}}
{{- end -}}

{{/* Returns the explicitly marked default domain, or the first configured domain. */}}
{{- define "podplane.traefik.defaultDomain" -}}
{{- $domains := .Values.platform.traefik.ingress.domains -}}
{{- $default := dict -}}
{{- range $domains -}}
{{- if .default -}}
{{- $default = . -}}
{{- end -}}
{{- end -}}
{{- if not $default -}}
{{- $default = first $domains -}}
{{- end -}}
{{- toJson $default -}}
{{- end -}}
