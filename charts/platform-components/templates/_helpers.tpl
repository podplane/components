{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{- define "podplane.platform.releaseName" -}}
{{- $name := . -}}
{{- if hasPrefix "platform-" $name -}}{{ $name }}{{- else -}}platform-{{ $name }}{{- end -}}
{{- end -}}

{{- define "podplane.platform.values" -}}
{{- $name := .name -}}
{{- $values := .values -}}
{{- $userValues := index $values $name | default dict -}}
{{- $mirrorValues := dict -}}
{{- if and .mirrorEnabled .mirrorHostname -}}
{{- $rendered := include "podplane.platform.imageMirrorValues" (dict "hostname" .mirrorHostname) -}}
{{- if $rendered -}}
{{- $allMirrorValues := fromYaml $rendered | default dict -}}
{{- $mirrorValues = index $allMirrorValues $name | default dict -}}
{{- end -}}
{{- end -}}
{{- $merged := mergeOverwrite (deepCopy $mirrorValues) $userValues -}}
{{- if $merged }}
  values:
{{ toYaml $merged | indent 4 }}
{{- end -}}
{{- end -}}
