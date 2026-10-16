{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0
*/}}

{{/*
platform.runtime.notStaticPodMirror is a CEL condition that excludes mirror Pods
the kubelet creates for its kube-system static Pods. Kubelet launches static
Pods with the node's default runtime, so their mirrors must not claim a
RuntimeClass. Only node identities may create mirror Pods, and NodeRestriction
prevents nodes from creating any other Pod, so tenant-supplied metadata alone
never matches.
*/}}
{{- define "platform.runtime.notStaticPodMirror" -}}
!(request.namespace == "kube-system" &&
  request.userInfo.username.startsWith("system:node:") &&
  request.userInfo.groups.exists(g, g == "system:nodes") &&
  object.metadata.?annotations[?"kubernetes.io/config.mirror"].hasValue())
{{- end -}}

{{/*
platform.runtime.runcDefaultNamespaces renders the runc default allowlist as a CEL
list. The default never extends to tenant namespaces.
*/}}
{{- define "platform.runtime.runcDefaultNamespaces" -}}
{{- $namespaces := .Values.platform.runtime.runcDefaultNamespaces | default list -}}
{{- range $namespaces }}
{{- if not (hasPrefix "platform-" .) }}
{{- fail (printf "platform.runtime.runcDefaultNamespaces may contain only platform-* namespaces, got %q" .) }}
{{- end }}
{{- end }}
{{- toJson $namespaces -}}
{{- end -}}
