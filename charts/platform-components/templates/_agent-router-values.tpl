{{/*
Podplane <https://podplane.dev>
Copyright The Podplane Authors
SPDX-License-Identifier: Apache-2.0

Envoy Gateway defaults for the Agent Router extension. The app release renderer
applies these when Agent Router is enabled; user component values take precedence.
*/}}

{{- define "podplane.platform.agentRouterValues" -}}
gateway-helm:
  config:
    envoyGateway:
      extensionApis:
        enableBackend: true
      extensionManager:
        hooks:
          xdsTranslator:
            translation:
              listener: {includeAll: true}
              route: {includeAll: true}
              cluster: {includeAll: true}
              secret: {includeAll: true}
            post: [Translation, Cluster, Route]
        service:
          fqdn:
            hostname: agent-router.{{ .namespace }}.svc.cluster.local
            port: 1063
{{- end -}}
