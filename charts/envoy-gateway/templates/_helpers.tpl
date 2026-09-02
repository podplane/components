{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0
*/}}
{{- define "podplane.envoyGateway.apex" -}}
{{- required "platform.envoyGateway.ingress.domains[].apex is required" (default .zone .apex) -}}
{{- end -}}

{{- define "podplane.envoyGateway.bundle" -}}
{{- printf "bundle-%s" (include "podplane.envoyGateway.apex" . | sha256sum | trunc 24) -}}
{{- end -}}

{{- define "podplane.envoyGateway.listenerSuffix" -}}
{{- $apex := include "podplane.envoyGateway.apex" . -}}
{{- printf "%s-%s" (regexReplaceAll "[^a-z0-9-]+" ($apex | lower) "-" | trunc 25 | trimAll "-") ($apex | sha256sum | trunc 8) -}}
{{- end -}}
